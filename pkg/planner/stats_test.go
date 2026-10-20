package planner_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
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

func TestTableStats_MultiColumnStatistics_CorrelatedSelectivity(t *testing.T) {
	ts := planner.NewTableStats("customers", 10000)

	// 10 distinct countries (sel = 1/10 = 0.10)
	countries := []string{"US", "KR", "JP", "DE", "FR", "UK", "CA", "AU", "IT", "ES"}
	cVals := make([]storage.Value, 10000)
	for i := 0; i < 10000; i++ {
		cVals[i] = storage.StringValue(countries[i%10])
	}
	ts.Columns["country"] = planner.BuildColumnStats("country", storage.TypeVarChar, cVals, 0, 10000)

	// 100 distinct cities (sel = 1/100 = 0.01)
	cityVals := make([]storage.Value, 10000)
	for i := 0; i < 10000; i++ {
		cityVals[i] = storage.StringValue(fmt.Sprintf("%s_City%d", countries[i%10], i%100))
	}
	ts.Columns["city"] = planner.BuildColumnStats("city", storage.TypeVarChar, cityVals, 0, 10000)

	// Baseline without multi-column statistics:
	// P(country = 'US') * P(city = 'US_City0') = 0.10 * 0.01 = 0.001 (10x underestimation!)
	selWithout := ts.EstimateCompoundSelectivity([]string{"country = 'US'", "city = 'US_City0'"})
	if selWithout > 0.002 {
		t.Fatalf("expected baseline selectivity ~0.001, got %f", selWithout)
	}

	// Add multi-column extended statistics on (country, city) with dependency = 1.0 (city determines country)
	ms := ts.AddMultiColumnStats("stat_geo", []string{"city", "country"}, 100, 1.0)
	if ms == nil {
		t.Fatal("expected non-nil MultiColumnStats")
	}

	// Verify FindMultiColumnStats is order-insensitive and case-insensitive
	found := ts.FindMultiColumnStats([]string{"Country", "City"})
	if found == nil || found.Name != "stat_geo" {
		t.Fatalf("expected to find stat_geo, got %+v", found)
	}

	// Verify joint selectivity via slice of condition strings
	selWithSlice := ts.EstimateCompoundSelectivity([]string{"country = 'US'", "city = 'US_City0'"})
	// With multi-column stats, joint selectivity is 0.01 (10x correction)
	if selWithSlice < 0.009 || selWithSlice > 0.011 {
		t.Fatalf("expected multi-column selectivity ~0.01, got %f", selWithSlice)
	}

	// Verify joint selectivity via composite AND expression
	selWithExpr := ts.EstimateCompoundSelectivity([]string{"country = 'US' AND city = 'US_City0'"})
	if selWithExpr < 0.009 || selWithExpr > 0.011 {
		t.Fatalf("expected expression multi-column selectivity ~0.01, got %f", selWithExpr)
	}

	// Verify non-correlated predicate combination:
	// country = 'US' AND city = 'US_City0' AND id <= 5000 (sel 0.5)
	idVals := make([]storage.Value, 10000)
	for i := 0; i < 10000; i++ {
		idVals[i] = storage.IntValue(int64(i + 1))
	}
	ts.Columns["id"] = planner.BuildColumnStats("id", storage.TypeInt, idVals, 0, 10000)

	selWithThird := ts.EstimateCompoundSelectivity([]string{"country = 'US'", "city = 'US_City0'", "id <= 5000"})
	// 0.01 * 0.50 = 0.005
	if selWithThird < 0.0045 || selWithThird > 0.0055 {
		t.Fatalf("expected combined 3-predicate selectivity ~0.005, got %f", selWithThird)
	}
}

func TestTableStats_MultiColumnStatistics_FunctionalDependency(t *testing.T) {
	colStats := map[string]*planner.ColumnStats{
		"a": {Name: "a", DistinctCount: 10},
		"b": {Name: "b", DistinctCount: 100},
	}

	// Case 1: Complete functional dependency (b determines a), multiNDV = max(10, 100) = 100
	depFull := planner.ComputeFunctionalDependency([]string{"a", "b"}, 100, colStats, 1000)
	if depFull != 1.0 {
		t.Errorf("expected full dependency 1.0, got %f", depFull)
	}

	// Case 2: Complete independence, multiNDV = 10 * 100 = 1000
	depIndep := planner.ComputeFunctionalDependency([]string{"a", "b"}, 1000, colStats, 10000)
	if depIndep != 0.0 {
		t.Errorf("expected independence 0.0, got %f", depIndep)
	}

	// Case 3: Partial correlation, multiNDV = 550
	depPartial := planner.ComputeFunctionalDependency([]string{"a", "b"}, 550, colStats, 10000)
	if depPartial < 0.45 || depPartial > 0.55 {
		t.Errorf("expected partial dependency ~0.50, got %f", depPartial)
	}
}

func TestTableStats_MostCommonValues_CategoricalSkew(t *testing.T) {
	// Scenario: 10,000 order rows with highly skewed status distribution:
	// 'ACTIVE': 8,000 rows (80%)
	// 'PENDING': 1,500 rows (15%)
	// 'DELETED': 500 rows (5%)
	ts := planner.NewTableStats("orders", 10000)

	vals := make([]storage.Value, 10000)
	for i := 0; i < 8000; i++ {
		vals[i] = storage.StringValue("ACTIVE")
	}
	for i := 8000; i < 9500; i++ {
		vals[i] = storage.StringValue("PENDING")
	}
	for i := 9500; i < 10000; i++ {
		vals[i] = storage.StringValue("DELETED")
	}

	cs := planner.BuildColumnStats("status", storage.TypeVarChar, vals, 0, 10000)
	ts.Columns["status"] = cs

	// 1. Verify MCV list extraction
	if len(cs.MCVList) != 3 {
		t.Fatalf("expected exactly 3 MCV items, got %d", len(cs.MCVList))
	}
	if cs.MCVList[0].Value.String() != "ACTIVE" || cs.MCVList[0].Frequency < 0.79 || cs.MCVList[0].Frequency > 0.81 {
		t.Errorf("expected top MCV 'ACTIVE' freq ~0.80, got %s freq %f", cs.MCVList[0].Value.String(), cs.MCVList[0].Frequency)
	}
	if cs.MCVList[1].Value.String() != "PENDING" || cs.MCVList[1].Frequency < 0.14 || cs.MCVList[1].Frequency > 0.16 {
		t.Errorf("expected 2nd MCV 'PENDING' freq ~0.15, got %s freq %f", cs.MCVList[1].Value.String(), cs.MCVList[1].Frequency)
	}
	if cs.MCVList[2].Value.String() != "DELETED" || cs.MCVList[2].Frequency < 0.04 || cs.MCVList[2].Frequency > 0.06 {
		t.Errorf("expected 3rd MCV 'DELETED' freq ~0.05, got %s freq %f", cs.MCVList[2].Value.String(), cs.MCVList[2].Frequency)
	}

	// 2. Exact equality selectivity using MCVs
	selActive := ts.EstimateSelectivity("status", "=", "ACTIVE")
	if selActive < 0.79 || selActive > 0.81 {
		t.Errorf("expected ACTIVE selectivity ~0.80, got %f", selActive)
	}

	selPending := ts.EstimateSelectivity("status", "=", "PENDING")
	if selPending < 0.14 || selPending > 0.16 {
		t.Errorf("expected PENDING selectivity ~0.15, got %f", selPending)
	}

	selDeleted := ts.EstimateSelectivity("status", "=", "DELETED")
	if selDeleted < 0.04 || selDeleted > 0.06 {
		t.Errorf("expected DELETED selectivity ~0.05, got %f", selDeleted)
	}

	// 3. Non-MCV unknown value equality
	selUnknown := ts.EstimateSelectivity("status", "=", "UNKNOWN")
	if selUnknown > 0.001 {
		t.Errorf("expected non-existent value selectivity ~0.0001 (minSel), got %f", selUnknown)
	}

	// 4. Inequality selectivity: != 'ACTIVE' -> 1.0 - 0.80 = 0.20
	selNotActive := ts.EstimateSelectivity("status", "!=", "ACTIVE")
	if selNotActive < 0.19 || selNotActive > 0.21 {
		t.Errorf("expected != 'ACTIVE' selectivity ~0.20, got %f", selNotActive)
	}

	// 5. IN clause evaluation
	condIn := ts.EstimateCompoundSelectivity([]string{"status IN ('ACTIVE', 'PENDING')"})
	// 0.80 + 0.15 = 0.95
	if condIn < 0.94 || condIn > 0.96 {
		t.Errorf("expected IN ('ACTIVE', 'PENDING') selectivity ~0.95, got %f", condIn)
	}
}

func TestTableStats_MostCommonValues_NumericSkewAndRange(t *testing.T) {
	// Scenario: 10,000 transaction amounts with heavy skew at 100 (70%) and 200 (20%),
	// plus a uniform tail of 1,000 distinct values from 1 to 1000 (10%).
	ts := planner.NewTableStats("txs", 10000)

	vals := make([]storage.Value, 0, 10000)
	for i := 0; i < 7000; i++ {
		vals = append(vals, storage.IntValue(100))
	}
	for i := 0; i < 2000; i++ {
		vals = append(vals, storage.IntValue(200))
	}
	for i := 1; i <= 1000; i++ {
		vals = append(vals, storage.IntValue(int64(i)))
	}

	cs := planner.BuildColumnStats("amount", storage.TypeInt, vals, 0, 10000)
	ts.Columns["amount"] = cs

	// Verify top MCVs are 100 (freq ~0.70) and 200 (freq ~0.20)
	if len(cs.MCVList) < 2 {
		t.Fatalf("expected at least 2 MCVs, got %d", len(cs.MCVList))
	}
	if cs.MCVList[0].Value.IVal != 100 || cs.MCVList[0].Frequency < 0.69 || cs.MCVList[0].Frequency > 0.71 {
		t.Errorf("expected top MCV 100 freq ~0.70, got %d freq %f", cs.MCVList[0].Value.IVal, cs.MCVList[0].Frequency)
	}
	if cs.MCVList[1].Value.IVal != 200 || cs.MCVList[1].Frequency < 0.19 || cs.MCVList[1].Frequency > 0.21 {
		t.Errorf("expected 2nd MCV 200 freq ~0.20, got %d freq %f", cs.MCVList[1].Value.IVal, cs.MCVList[1].Frequency)
	}

	// 1. Equality: amount = 100 -> ~0.70
	sel100 := ts.EstimateSelectivity("amount", "=", "100")
	if sel100 < 0.69 || sel100 > 0.71 {
		t.Errorf("expected amount=100 selectivity ~0.70, got %f", sel100)
	}

	// 2. Equality on rare non-MCV value: amount = 555 -> very small selectivity
	selRare := ts.EstimateSelectivity("amount", "=", "555")
	if selRare > 0.005 {
		t.Errorf("expected rare tail selectivity < 0.005, got %f", selRare)
	}

	// 3. Range selectivity: amount < 150
	// Includes MCV 100 (0.70) + portion of tail 1..1000 < 150 (~15% of 10% = 0.015) -> ~0.715
	selRange := ts.EstimateSelectivity("amount", "<", "150")
	if selRange < 0.68 || selRange > 0.75 {
		t.Errorf("expected amount < 150 selectivity ~0.715, got %f", selRange)
	}

	// 4. BETWEEN selectivity: amount BETWEEN 150 AND 250
	// Includes MCV 200 (0.20) + tail between 150 and 250 (~10% of 10% = 0.010) -> ~0.210
	selBetween := ts.EstimateCompoundSelectivity([]string{"amount BETWEEN 150 AND 250"})
	if selBetween < 0.18 || selBetween > 0.24 {
		t.Errorf("expected amount BETWEEN 150 AND 250 selectivity ~0.21, got %f", selBetween)
	}
}

func TestTableStats_MostCommonValues_UniformAndSingleValue(t *testing.T) {
	// Case 1: All distinct unique values (e.g. Primary Key 1..1000)
	ts1 := planner.NewTableStats("users", 1000)
	idVals := make([]storage.Value, 1000)
	for i := 0; i < 1000; i++ {
		idVals[i] = storage.IntValue(int64(i + 1))
	}
	cs1 := planner.BuildColumnStats("id", storage.TypeInt, idVals, 0, 1000)
	ts1.Columns["id"] = cs1

	// No MCVs should be extracted for uniform distinct values
	if len(cs1.MCVList) != 0 {
		t.Errorf("expected 0 MCVs for unique column, got %d", len(cs1.MCVList))
	}
	selEq := ts1.EstimateSelectivity("id", "=", "42")
	if selEq < 0.0009 || selEq > 0.0011 {
		t.Errorf("expected uniform selectivity ~0.001, got %f", selEq)
	}

	// Case 2: 100% Homogeneous column (all 1000 rows have 'ACTIVE')
	ts2 := planner.NewTableStats("settings", 1000)
	homoVals := make([]storage.Value, 1000)
	for i := 0; i < 1000; i++ {
		homoVals[i] = storage.StringValue("ACTIVE")
	}
	cs2 := planner.BuildColumnStats("status", storage.TypeVarChar, homoVals, 0, 1000)
	ts2.Columns["status"] = cs2

	if len(cs2.MCVList) != 1 {
		t.Fatalf("expected 1 MCV for homogeneous column, got %d", len(cs2.MCVList))
	}
	if cs2.MCVList[0].Frequency != 1.0 || cs2.MCVList[0].Count != 1000 {
		t.Errorf("expected frequency 1.0 and count 1000, got %f and %d", cs2.MCVList[0].Frequency, cs2.MCVList[0].Count)
	}
	selHomo := ts2.EstimateSelectivity("status", "=", "ACTIVE")
	if selHomo != 1.0 {
		t.Errorf("expected 1.0 selectivity for homogeneous column, got %f", selHomo)
	}
}

func TestTableStats_MostCommonValues_JSONRoundTrip(t *testing.T) {
	ts := planner.NewTableStats("inventory", 1000)
	vals := make([]storage.Value, 1000)
	for i := 0; i < 800; i++ {
		vals[i] = storage.StringValue("IN_STOCK")
	}
	for i := 800; i < 1000; i++ {
		vals[i] = storage.StringValue("BACKORDER")
	}
	cs := planner.BuildColumnStats("state", storage.TypeVarChar, vals, 0, 1000)
	ts.Columns["state"] = cs

	// Serialize TableStats to JSON
	data, err := json.Marshal(ts)
	if err != nil {
		t.Fatalf("failed to marshal TableStats: %v", err)
	}

	// Deserialize back
	var restored planner.TableStats
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("failed to unmarshal TableStats: %v", err)
	}

	restoredCol, ok := restored.Columns["state"]
	if !ok || restoredCol == nil {
		t.Fatalf("restored state column stats missing")
	}
	if len(restoredCol.MCVList) != 2 {
		t.Fatalf("expected 2 MCVs after restore, got %d", len(restoredCol.MCVList))
	}
	if restoredCol.MCVList[0].Value.String() != "IN_STOCK" || restoredCol.MCVList[0].Frequency < 0.79 {
		t.Errorf("restored MCV0 mismatch: %+v", restoredCol.MCVList[0])
	}

	// Selectivity after restore must be identical
	sel := restored.EstimateSelectivity("state", "=", "IN_STOCK")
	if sel < 0.79 || sel > 0.81 {
		t.Errorf("expected restored selectivity ~0.80, got %f", sel)
	}
}



