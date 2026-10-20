package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestMerge_IdentityAutoGeneration(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create target table with IDENTITY primary key
	ddlTarget := `
	CREATE TABLE target_products (
		id INT IDENTITY PRIMARY KEY,
		sku VARCHAR(50) NOT NULL,
		price INT NOT NULL
	);
	`
	if _, err := coord.ExecuteSQL(ddlTarget); err != nil {
		t.Fatalf("create target table failed: %v", err)
	}

	// 2. Create source table with sku and price (omitting id)
	ddlSource := `
	CREATE TABLE source_products (
		sku VARCHAR(50) PRIMARY KEY,
		price INT NOT NULL
	);
	`
	if _, err := coord.ExecuteSQL(ddlSource); err != nil {
		t.Fatalf("create source table failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("INSERT INTO source_products (sku, price) VALUES ('SKU-A', 100), ('SKU-B', 200)"); err != nil {
		t.Fatalf("insert source failed: %v", err)
	}

	// 3. Execute MERGE with WHEN NOT MATCHED THEN INSERT (sku, price)
	mergeSQL := `
	MERGE target_products AS t
	USING source_products AS s
	ON t.sku = s.sku
	WHEN MATCHED THEN
		UPDATE SET price = s.price
	WHEN NOT MATCHED THEN
		INSERT (sku, price) VALUES (s.sku, s.price);
	`
	res, err := coord.ExecuteSQL(mergeSQL)
	if err != nil {
		t.Fatalf("MERGE execution failed: %v", err)
	}
	if res.RowsAffected != 2 {
		t.Errorf("expected 2 rows affected, got %d", res.RowsAffected)
	}

	// 4. Verify that target_products has auto-generated IDENTITY values
	selRes, err := coord.ExecuteSQL("SELECT id, sku, price FROM target_products ORDER BY id")
	if err != nil {
		t.Fatalf("SELECT target failed: %v", err)
	}
	if len(selRes.Rows) != 2 {
		t.Fatalf("expected 2 rows in target_products, got %d", len(selRes.Rows))
	}

	id1 := fmt.Sprintf("%v", selRes.Rows[0]["id"])
	sku1 := fmt.Sprintf("%v", selRes.Rows[0]["sku"])
	if id1 != "1" || sku1 != "SKU-A" {
		t.Errorf("expected row 1: (1, SKU-A), got (%v, %v)", id1, sku1)
	}

	id2 := fmt.Sprintf("%v", selRes.Rows[1]["id"])
	sku2 := fmt.Sprintf("%v", selRes.Rows[1]["sku"])
	if id2 != "2" || sku2 != "SKU-B" {
		t.Errorf("expected row 2: (2, SKU-B), got (%v, %v)", id2, sku2)
	}
}

func TestMerge_NotMatchedBySource_DoesNotDeleteNewlyInserted(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE target_sync (id INT PRIMARY KEY, val VARCHAR(50))"); err != nil {
		t.Fatalf("create target table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE source_sync (id INT PRIMARY KEY, val VARCHAR(50))"); err != nil {
		t.Fatalf("create source table failed: %v", err)
	}

	// Target initially contains row (1, 'old')
	if _, err := coord.ExecuteSQL("INSERT INTO target_sync (id, val) VALUES (1, 'old')"); err != nil {
		t.Fatalf("insert target failed: %v", err)
	}

	// Source contains row (2, 'new')
	if _, err := coord.ExecuteSQL("INSERT INTO source_sync (id, val) VALUES (2, 'new')"); err != nil {
		t.Fatalf("insert source failed: %v", err)
	}

	// MERGE with WHEN NOT MATCHED THEN INSERT AND WHEN NOT MATCHED BY SOURCE THEN DELETE
	mergeSQL := `
	MERGE target_sync AS t
	USING source_sync AS s
	ON t.id = s.id
	WHEN MATCHED THEN
		UPDATE SET val = s.val
	WHEN NOT MATCHED THEN
		INSERT (id, val) VALUES (s.id, s.val)
	WHEN NOT MATCHED BY SOURCE THEN
		DELETE;
	`
	res, err := coord.ExecuteSQL(mergeSQL)
	if err != nil {
		t.Fatalf("MERGE failed: %v", err)
	}
	// Row 1 should be deleted (not matched by source), and Row 2 should be inserted (not matched by target)
	if res.RowsAffected != 2 {
		t.Errorf("expected 2 rows affected, got %d", res.RowsAffected)
	}

	// Verify target_sync now contains ONLY (2, 'new') - row 2 must NOT have been deleted by NOT MATCHED BY SOURCE!
	selRes, err := coord.ExecuteSQL("SELECT id, val FROM target_sync")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(selRes.Rows) != 1 {
		t.Fatalf("expected exactly 1 row in target_sync, got %d: %+v", len(selRes.Rows), selRes.Rows)
	}
	if fmt.Sprintf("%v", selRes.Rows[0]["id"]) != "2" || fmt.Sprintf("%v", selRes.Rows[0]["val"]) != "new" {
		t.Errorf("expected remaining row to be (2, 'new'), got %+v", selRes.Rows[0])
	}
}

func TestPivot_CountZeroForEmptyCategories(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE sales (id INT PRIMARY KEY, category VARCHAR(20), amount INT)"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	// Only insert records for 'Book' and 'Toy', none for 'Food'
	if _, err := coord.ExecuteSQL("INSERT INTO sales (id, category, amount) VALUES (1, 'Book', 10), (2, 'Book', 20), (3, 'Toy', 30)"); err != nil {
		t.Fatalf("insert sales failed: %v", err)
	}

	pivotSQL := `
	SELECT [Book], [Toy], [Food]
	FROM (
		SELECT category, amount FROM sales
	) AS src
	PIVOT (
		COUNT(amount)
		FOR category IN ([Book], [Toy], [Food])
	) AS pvt;
	`
	res, err := coord.ExecuteSQL(pivotSQL)
	if err != nil {
		t.Fatalf("PIVOT failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	// Food category has 0 rows, so COUNT must return 0, not NULL!
	foodVal := fmt.Sprintf("%v", res.Rows[0]["Food"])
	if foodVal != "0" {
		t.Errorf("expected COUNT for empty category Food to be 0, got %v", foodVal)
	}
	bookVal := fmt.Sprintf("%v", res.Rows[0]["Book"])
	if bookVal != "2" {
		t.Errorf("expected COUNT for Book to be 2, got %v", bookVal)
	}
	toyVal := fmt.Sprintf("%v", res.Rows[0]["Toy"])
	if toyVal != "1" {
		t.Errorf("expected COUNT for Toy to be 1, got %v", toyVal)
	}
}

func TestForJSON_TableAliasNotWrapped(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50))"); err != nil {
		t.Fatalf("create users failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO users (id, name) VALUES (1, 'Alice')"); err != nil {
		t.Fatalf("insert users failed: %v", err)
	}

	// Query with table alias 'u': SELECT u.id, u.name FROM users u FOR JSON PATH
	query := "SELECT u.id, u.name FROM users u FOR JSON PATH"
	res, err := coord.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("FOR JSON failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	jsonStr := fmt.Sprintf("%v", res.Rows[0]["JSON_F52E2B61-18A1-11d1-B105-00805F49916B"])
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("failed to parse JSON result %q: %v", jsonStr, err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 item in JSON array, got %d", len(parsed))
	}

	// 'u' should not be a wrapper object!
	if _, hasU := parsed[0]["u"]; hasU {
		t.Fatalf("table alias 'u' was incorrectly wrapped into nested object: %+v", parsed[0])
	}
	if fmt.Sprintf("%v", parsed[0]["id"]) != "1" || fmt.Sprintf("%v", parsed[0]["name"]) != "Alice" {
		t.Errorf("unexpected json contents: %+v", parsed[0])
	}

	// Explicit dot-alias [user.id] should still create nested object
	queryDot := "SELECT u.id AS [account.id], u.name AS [account.name] FROM users u FOR JSON PATH"
	resDot, err := coord.ExecuteSQL(queryDot)
	if err != nil {
		t.Fatalf("FOR JSON explicit dot failed: %v", err)
	}
	jsonStrDot := fmt.Sprintf("%v", resDot.Rows[0]["JSON_F52E2B61-18A1-11d1-B105-00805F49916B"])
	var parsedDot []map[string]any
	if err := json.Unmarshal([]byte(jsonStrDot), &parsedDot); err != nil {
		t.Fatalf("failed to parse dot JSON: %v", err)
	}
	accountObj, ok := parsedDot[0]["account"].(map[string]any)
	if !ok || fmt.Sprintf("%v", accountObj["id"]) != "1" {
		t.Errorf("expected nested account object, got: %+v", parsedDot[0])
	}
}

func TestDerivedTable_JoinSelectStar_PreservesAllColumns(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE orders (order_id INT PRIMARY KEY, user_id INT, amount INT)"); err != nil {
		t.Fatalf("create orders failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50))"); err != nil {
		t.Fatalf("create users failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("INSERT INTO orders (order_id, user_id, amount) VALUES (101, 1, 500)"); err != nil {
		t.Fatalf("insert orders failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO users (id, name) VALUES (1, 'Alice')"); err != nil {
		t.Fatalf("insert users failed: %v", err)
	}

	// SELECT * from orders o JOIN (SELECT id, name FROM users) d ON o.user_id = d.id
	query := `SELECT * FROM orders o JOIN (SELECT id, name FROM users) d ON o.user_id = d.id`
	res, err := coord.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("derived table join query failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	// Columns from orders (order_id, amount) must NOT have evaporated!
	row := res.Rows[0]
	if fmt.Sprintf("%v", row["order_id"]) != "101" {
		t.Errorf("expected order_id 101, got %v", row["order_id"])
	}
	if fmt.Sprintf("%v", row["amount"]) != "500" {
		t.Errorf("expected amount 500, got %v", row["amount"])
	}
	if fmt.Sprintf("%v", row["name"]) != "Alice" {
		t.Errorf("expected name Alice, got %v", row["name"])
	}
	// Synthetic PK _dt_row_id must not leak into user projection
	if _, hasSyn := row["_dt_row_id"]; hasSyn {
		t.Errorf("synthetic PK _dt_row_id leaked into output row: %+v", row)
	}
}

func TestApply_TokenAwareParameterReplacement(t *testing.T) {
	row := map[string]any{
		"id":     42,
		"status": "active",
	}
	tbl := proc.TableRef{
		IsTVF: true,
		Name:  "GetUserDetails",
		TVFArgs: []proc.Expr{
			&proc.ColumnRefExpr{Name: "u.id"},
			&proc.LiteralExpr{Val: storage.StringValue("status of id is ok")},
		},
	}
	query, err := buildParameterizedApplyQuery(tbl, row, "u", "users")
	if err != nil {
		t.Fatalf("buildParameterizedApplyQuery failed: %v", err)
	}
	// 'u.id' must be substituted with 42
	if !strings.Contains(query, "42") {
		t.Errorf("expected 42 in query, got: %s", query)
	}
	// 'status of id is ok' must be preserved as literal string without substituting 'id' or 'status'
	if !strings.Contains(query, "'status of id is ok'") {
		t.Errorf("expected string literal 'status of id is ok' to be preserved, got: %s", query)
	}
}
