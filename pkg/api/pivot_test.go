package api_test

import (
	"testing"
)

func TestPivot_BasicSum(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	execStatements(t, server,
		"CREATE TABLE quarterly_sales (id INT PRIMARY KEY, year INT, quarter VARCHAR(10), amount INT)",
		"INSERT INTO quarterly_sales VALUES (1, 2023, 'Q1', 100)",
		"INSERT INTO quarterly_sales VALUES (2, 2023, 'Q2', 150)",
		"INSERT INTO quarterly_sales VALUES (3, 2023, 'Q3', 200)",
		"INSERT INTO quarterly_sales VALUES (4, 2023, 'Q4', 250)",
		"INSERT INTO quarterly_sales VALUES (5, 2024, 'Q1', 120)",
		"INSERT INTO quarterly_sales VALUES (6, 2024, 'Q2', 180)",
		"INSERT INTO quarterly_sales VALUES (7, 2024, 'Q3', 220)",
		"INSERT INTO quarterly_sales VALUES (8, 2024, 'Q4', 300)",
	)

	query := `
		SELECT year, [Q1], [Q2], [Q3], [Q4]
		FROM (
			SELECT year, quarter, amount FROM quarterly_sales
		) AS src
		PIVOT (
			SUM(amount)
			FOR quarter IN ([Q1], [Q2], [Q3], [Q4])
		) AS pvt
		ORDER BY year ASC;
	`
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("PIVOT query failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(res.Rows), res.Rows)
	}

	// 2023 row
	row2023 := res.Rows[0]
	if row2023["Q1"] != int64(100) || row2023["Q2"] != int64(150) || row2023["Q3"] != int64(200) || row2023["Q4"] != int64(250) {
		t.Errorf("2023 unexpected row values: %v", row2023)
	}

	// 2024 row
	row2024 := res.Rows[1]
	if row2024["Q1"] != int64(120) || row2024["Q2"] != int64(180) || row2024["Q3"] != int64(220) || row2024["Q4"] != int64(300) {
		t.Errorf("2024 unexpected row values: %v", row2024)
	}
}

func TestPivot_CountAndDirectTable(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	execStatements(t, server,
		"CREATE TABLE emp_dept (id INT PRIMARY KEY, dept VARCHAR(30), level VARCHAR(10))",
		"INSERT INTO emp_dept VALUES (1, 'Engineering', 'Junior')",
		"INSERT INTO emp_dept VALUES (2, 'Engineering', 'Junior')",
		"INSERT INTO emp_dept VALUES (3, 'Engineering', 'Senior')",
		"INSERT INTO emp_dept VALUES (4, 'Marketing',   'Junior')",
		"INSERT INTO emp_dept VALUES (5, 'Marketing',   'Senior')",
		"INSERT INTO emp_dept VALUES (6, 'Marketing',   'Senior')",
		"INSERT INTO emp_dept VALUES (7, 'HR',          'Junior')",
	)

	// Direct table source without derived subquery
	query := `
		SELECT dept, [Junior], [Senior]
		FROM emp_dept
		PIVOT (
			COUNT(id)
			FOR level IN ([Junior], [Senior])
		) AS pvt
		ORDER BY dept ASC;
	`
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("PIVOT query failed: %v", err)
	}

	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 departments, got %d: %v", len(res.Rows), res.Rows)
	}

	// Map of dept -> Junior, Senior counts
	deptMap := make(map[string]map[string]int)
	for _, r := range res.Rows {
		d := r["dept"].(string)
		deptMap[d] = map[string]int{
			"Junior": 0,
			"Senior": 0,
		}
		if r["Junior"] != nil {
			deptMap[d]["Junior"] = r["Junior"].(int)
		}
		if r["Senior"] != nil {
			deptMap[d]["Senior"] = r["Senior"].(int)
		}
	}

	if deptMap["Engineering"]["Junior"] != 2 || deptMap["Engineering"]["Senior"] != 1 {
		t.Errorf("Engineering counts mismatch: %v", deptMap["Engineering"])
	}
	if deptMap["Marketing"]["Junior"] != 1 || deptMap["Marketing"]["Senior"] != 2 {
		t.Errorf("Marketing counts mismatch: %v", deptMap["Marketing"])
	}
	if deptMap["HR"]["Junior"] != 1 || deptMap["HR"]["Senior"] != 0 {
		t.Errorf("HR counts mismatch: %v", deptMap["HR"])
	}
}

func TestPivot_AliasesAndWhereAndOrder(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	execStatements(t, server,
		"CREATE TABLE product_metrics (id INT PRIMARY KEY, region VARCHAR(20), metric_type VARCHAR(20), score INT)",
		"INSERT INTO product_metrics VALUES (1, 'North', 'Speed',   90)",
		"INSERT INTO product_metrics VALUES (2, 'North', 'Quality', 85)",
		"INSERT INTO product_metrics VALUES (3, 'South', 'Speed',   70)",
		"INSERT INTO product_metrics VALUES (4, 'South', 'Quality', 95)",
		"INSERT INTO product_metrics VALUES (5, 'East',  'Speed',   60)",
		"INSERT INTO product_metrics VALUES (6, 'East',  'Quality', 65)",
	)

	// Using aliases in outer SELECT and WHERE filter
	query := `
		SELECT region, [Speed] AS SpeedScore, [Quality] AS QualityScore
		FROM (
			SELECT region, metric_type, score FROM product_metrics
		) src
		PIVOT (
			AVG(score)
			FOR metric_type IN ([Speed], [Quality])
		) pvt
		WHERE [Speed] >= 70
		ORDER BY region DESC;
	`
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("PIVOT query failed: %v", err)
	}

	// Only North (90) and South (70) should match Speed >= 70
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(res.Rows), res.Rows)
	}

	// Check columns
	expectedCols := []string{"region", "SpeedScore", "QualityScore"}
	for i, c := range res.Columns {
		if c != expectedCols[i] {
			t.Errorf("column %d: expected %s, got %s", i, expectedCols[i], c)
		}
	}

	// Verify DESC order: South then North
	if res.Rows[0]["region"] != "South" || res.Rows[1]["region"] != "North" {
		t.Errorf("unexpected region order: %v, %v", res.Rows[0]["region"], res.Rows[1]["region"])
	}
}

func TestPivot_MinMaxAggregates(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	execStatements(t, server,
		"CREATE TABLE vendor_orders (id INT PRIMARY KEY, vendor_id INT, cat VARCHAR(20), price INT)",
		"INSERT INTO vendor_orders VALUES (1, 101, 'Hardware', 500)",
		"INSERT INTO vendor_orders VALUES (2, 101, 'Hardware', 300)",
		"INSERT INTO vendor_orders VALUES (3, 101, 'Software', 200)",
		"INSERT INTO vendor_orders VALUES (4, 102, 'Hardware', 700)",
		"INSERT INTO vendor_orders VALUES (5, 102, 'Software', 400)",
	)

	query := `
		SELECT vendor_id, [Hardware], [Software]
		FROM (
			SELECT vendor_id, cat, price FROM vendor_orders
		) src
		PIVOT (
			MAX(price)
			FOR cat IN ([Hardware], [Software])
		) pvt
		ORDER BY vendor_id ASC;
	`
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("PIVOT MAX failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}

	// Vendor 101: Hardware MAX is 500, Software MAX is 200
	if res.Rows[0]["Hardware"] != int64(500) || res.Rows[0]["Software"] != int64(200) {
		t.Errorf("Vendor 101 values mismatch: %v", res.Rows[0])
	}
	// Vendor 102: Hardware MAX is 700, Software MAX is 400
	if res.Rows[1]["Hardware"] != int64(700) || res.Rows[1]["Software"] != int64(400) {
		t.Errorf("Vendor 102 values mismatch: %v", res.Rows[1])
	}
}

func TestUnpivot_Basic(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	execStatements(t, server,
		"CREATE TABLE wide_sales (id INT PRIMARY KEY, year INT, Q1 INT, Q2 INT, Q3 INT, Q4 INT)",
		"INSERT INTO wide_sales VALUES (1, 2023, 100, 150, 200, 250)",
		"INSERT INTO wide_sales VALUES (2, 2024, 120, 180, 220, 300)",
	)

	query := `
		SELECT year, quarter, amount
		FROM wide_sales
		UNPIVOT (
			amount FOR quarter IN (Q1, Q2, Q3, Q4)
		) AS unpvt
		ORDER BY year ASC, quarter ASC;
	`
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("UNPIVOT query failed: %v", err)
	}

	if len(res.Rows) != 8 {
		t.Fatalf("expected 8 unpivoted rows, got %d: %v", len(res.Rows), res.Rows)
	}

	// Verify first row: 2023, Q1, 100
	r0 := res.Rows[0]
	if r0["year"] != int64(2023) || r0["quarter"] != "Q1" || r0["amount"] != int64(100) {
		t.Errorf("row 0 mismatch: %v", r0)
	}

	// Verify last row: 2024, Q4, 300
	r7 := res.Rows[7]
	if r7["year"] != int64(2024) || r7["quarter"] != "Q4" || r7["amount"] != int64(300) {
		t.Errorf("row 7 mismatch: %v", r7)
	}
}

func TestUnpivot_WithSubqueryAndBracketSyntax(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	execStatements(t, server,
		"CREATE TABLE customer_scores (cust_id INT PRIMARY KEY, [Math] INT, [English] INT, [Science] INT)",
		"INSERT INTO customer_scores VALUES (101, 95, 88, 92)",
		"INSERT INTO customer_scores VALUES (102, 70, 85, 80)",
	)

	query := `
		SELECT cust_id, Subject, Score
		FROM (
			SELECT cust_id, [Math], [English], [Science] FROM customer_scores
		) AS src
		UNPIVOT (
			Score FOR Subject IN ([Math], [English], [Science])
		) AS unpvt
		WHERE Score >= 85
		ORDER BY Score DESC;
	`
	res, err := server.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("UNPIVOT with subquery and brackets failed: %v", err)
	}

	// Scores >= 85: 95 (Math, 101), 92 (Science, 101), 88 (English, 101), 85 (English, 102) -> 4 rows
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 filtered rows, got %d: %v", len(res.Rows), res.Rows)
	}

	if res.Rows[0]["Score"] != int64(95) {
		t.Errorf("expected highest score 95, got %v", res.Rows[0]["Score"])
	}
	if res.Rows[3]["Score"] != int64(85) {
		t.Errorf("expected lowest matching score 85, got %v", res.Rows[3]["Score"])
	}
}
