package api_test

import (
	"fmt"
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func setupSalesTestServer(t *testing.T) (*api.Server, func()) {
	eng := memengine.New()
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)

	_, err := server.ExecuteSQL(`CREATE TABLE sales (
		id INT PRIMARY KEY,
		region VARCHAR(50),
		product VARCHAR(50),
		amount INT
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO sales (id, region, product, amount) VALUES
		(1, 'North', 'Phone', 100),
		(2, 'North', 'Computer', 200),
		(3, 'South', 'Phone', 300),
		(4, 'South', 'Computer', 400);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	return server, func() {
		eng.Close()
	}
}

func TestSQLExecutor_GroupByRollup_SingleColumn(t *testing.T) {
	server, cleanup := setupSalesTestServer(t)
	defer cleanup()

	q := `SELECT region, SUM(amount) AS total, COUNT(*) AS cnt FROM sales GROUP BY ROLLUP(region);`
	res, err := server.ExecuteSQL(q)
	if err != nil || !res.Success {
		t.Fatalf("Query failed: %v", err)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows (2 regions + 1 grand total), got %d: %+v", len(res.Rows), res.Rows)
	}

	// First two rows are North and South, third row is Grand Total (NULL)
	foundNorth, foundSouth, foundGrand := false, false, false
	for _, row := range res.Rows {
		reg := row["region"]
		tot := fmt.Sprintf("%v", row["total"])
		cnt := fmt.Sprintf("%v", row["cnt"])

		if reg == "North" {
			foundNorth = true
			if tot != "300" || cnt != "2" {
				t.Errorf("North unexpected: total=%v cnt=%v", tot, cnt)
			}
		} else if reg == "South" {
			foundSouth = true
			if tot != "700" || cnt != "2" {
				t.Errorf("South unexpected: total=%v cnt=%v", tot, cnt)
			}
		} else if reg == nil {
			foundGrand = true
			if tot != "1000" || cnt != "4" {
				t.Errorf("Grand total unexpected: total=%v cnt=%v", tot, cnt)
			}
		}
	}

	if !foundNorth || !foundSouth || !foundGrand {
		t.Errorf("missing expected rows: north=%v south=%v grand=%v", foundNorth, foundSouth, foundGrand)
	}
}

func TestSQLExecutor_GroupByRollup_MultiColumnAndGrouping(t *testing.T) {
	server, cleanup := setupSalesTestServer(t)
	defer cleanup()

	q := `SELECT region, product, SUM(amount) AS total, GROUPING(region) AS grp_reg, GROUPING(product) AS grp_prod FROM sales GROUP BY ROLLUP(region, product);`
	res, err := server.ExecuteSQL(q)
	if err != nil || !res.Success {
		t.Fatalf("Query failed: %v", err)
	}

	// 4 details + 2 region subtotals + 1 grand total = 7 rows
	if len(res.Rows) != 7 {
		t.Fatalf("expected 7 rows, got %d: %+v", len(res.Rows), res.Rows)
	}

	subtotalsChecked := 0
	grandTotalChecked := 0
	for _, row := range res.Rows {
		reg := row["region"]
		prod := row["product"]
		tot := fmt.Sprintf("%v", row["total"])
		grpReg := fmt.Sprintf("%v", row["grp_reg"])
		grpProd := fmt.Sprintf("%v", row["grp_prod"])

		if reg == "North" && prod == nil {
			subtotalsChecked++
			if tot != "300" || grpReg != "0" || grpProd != "1" {
				t.Errorf("North subtotal mismatch: tot=%v grpReg=%v grpProd=%v", tot, grpReg, grpProd)
			}
		} else if reg == "South" && prod == nil {
			subtotalsChecked++
			if tot != "700" || grpReg != "0" || grpProd != "1" {
				t.Errorf("South subtotal mismatch: tot=%v grpReg=%v grpProd=%v", tot, grpReg, grpProd)
			}
		} else if reg == nil && prod == nil {
			grandTotalChecked++
			if tot != "1000" || grpReg != "1" || grpProd != "1" {
				t.Errorf("Grand total mismatch: tot=%v grpReg=%v grpProd=%v", tot, grpReg, grpProd)
			}
		}
	}

	if subtotalsChecked != 2 {
		t.Errorf("expected 2 subtotals, found %d", subtotalsChecked)
	}
	if grandTotalChecked != 1 {
		t.Errorf("expected 1 grand total, found %d", grandTotalChecked)
	}
}

func TestSQLExecutor_GroupByRollup_WithRollupTSqlSyntax(t *testing.T) {
	server, cleanup := setupSalesTestServer(t)
	defer cleanup()

	q := `SELECT region, SUM(amount) AS total FROM sales GROUP BY region WITH ROLLUP;`
	res, err := server.ExecuteSQL(q)
	if err != nil || !res.Success {
		t.Fatalf("Query failed: %v", err)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows for WITH ROLLUP, got %d", len(res.Rows))
	}
}

func TestSQLExecutor_GroupByCube(t *testing.T) {
	server, cleanup := setupSalesTestServer(t)
	defer cleanup()

	q := `SELECT region, product, SUM(amount) AS total FROM sales GROUP BY CUBE(region, product);`
	res, err := server.ExecuteSQL(q)
	if err != nil || !res.Success {
		t.Fatalf("Query failed: %v", err)
	}

	// CUBE(2 cols) = 4 details + 2 region subtotals + 2 product subtotals + 1 grand total = 9 rows
	if len(res.Rows) != 9 {
		t.Fatalf("expected 9 rows for CUBE, got %d: %+v", len(res.Rows), res.Rows)
	}

	// Verify product subtotals (region is NULL, product is Phone or Computer)
	phoneTotal := ""
	compTotal := ""
	grandTotal := ""
	for _, row := range res.Rows {
		if row["region"] == nil && row["product"] == "Phone" {
			phoneTotal = fmt.Sprintf("%v", row["total"])
		} else if row["region"] == nil && row["product"] == "Computer" {
			compTotal = fmt.Sprintf("%v", row["total"])
		} else if row["region"] == nil && row["product"] == nil {
			grandTotal = fmt.Sprintf("%v", row["total"])
		}
	}

	if phoneTotal != "400" {
		t.Errorf("expected Phone cross-product total 400, got %v", phoneTotal)
	}
	if compTotal != "600" {
		t.Errorf("expected Computer cross-product total 600, got %v", compTotal)
	}
	if grandTotal != "1000" {
		t.Errorf("expected Grand total 1000, got %v", grandTotal)
	}
}

func TestSQLExecutor_GroupByRollup_HavingFilter(t *testing.T) {
	server, cleanup := setupSalesTestServer(t)
	defer cleanup()

	q := `SELECT region, SUM(amount) AS total FROM sales GROUP BY ROLLUP(region) HAVING SUM(amount) >= 700;`
	res, err := server.ExecuteSQL(q)
	if err != nil || !res.Success {
		t.Fatalf("Query failed: %v", err)
	}

	// North (300) should be excluded; South (700) and Grand Total (1000) should be present
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows meeting HAVING >= 700, got %d: %+v", len(res.Rows), res.Rows)
	}
}
