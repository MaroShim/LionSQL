package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// TestGroup3_DDL_SyntheticRowID tests BUG-DDL-PK-1: tables created without PK
// do NOT promote user columns (columns[0]) to PK; instead, a synthetic _row_id is injected.
func TestGroup3_DDL_SyntheticRowID(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create table without explicit PK
	if _, err := coord.ExecuteSQL("CREATE TABLE app_logs (message VARCHAR(255), severity INT);"); err != nil {
		t.Fatalf("CREATE TABLE app_logs failed: %v", err)
	}

	tbl, err := eng.GetTable("app_logs")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}

	// Verify user columns were NOT promoted to PK
	msgCol, _, foundMsg := tbl.FindColumn("message")
	if !foundMsg || msgCol.PrimaryKey {
		t.Fatalf("expected message column NOT to be primary key, got: %+v", msgCol)
	}

	// Verify synthetic _row_id exists as PK
	pkCol, err := tbl.PrimaryKeyColumn()
	if err != nil || pkCol == nil || pkCol.Name != "_row_id" {
		t.Fatalf("expected _row_id to be primary key, got: %v, err: %v", pkCol, err)
	}

	// 2. Insert multiple identical rows (would fail with unique violation if message was PK)
	insertSQL := "INSERT INTO app_logs (message, severity) VALUES ('server started', 1), ('server started', 1), ('server started', 2);"
	if _, err := coord.ExecuteSQL(insertSQL); err != nil {
		t.Fatalf("INSERT duplicate rows into app_logs failed: %v", err)
	}

	// 3. Verify SELECT * excludes hidden _row_id
	selRes, err := coord.ExecuteSQL("SELECT * FROM app_logs;")
	if err != nil {
		t.Fatalf("SELECT * failed: %v", err)
	}
	if len(selRes.Columns) != 2 || selRes.Columns[0] != "message" || selRes.Columns[1] != "severity" {
		t.Fatalf("expected 2 columns (message, severity), got: %v", selRes.Columns)
	}
	if len(selRes.Rows) != 3 {
		t.Fatalf("expected 3 rows, got: %d", len(selRes.Rows))
	}

	// 4. Verify explicit selection of _row_id works
	rowIDRes, err := coord.ExecuteSQL("SELECT _row_id, message FROM app_logs;")
	if err != nil {
		t.Fatalf("SELECT _row_id failed: %v", err)
	}
	if len(rowIDRes.Rows) != 3 {
		t.Fatalf("expected 3 rows for _row_id, got: %d", len(rowIDRes.Rows))
	}

	// 5. Verify UPDATE and DELETE on non-PK table
	if _, err := coord.ExecuteSQL("UPDATE app_logs SET severity = 10 WHERE severity = 2;"); err != nil {
		t.Fatalf("UPDATE non-PK table failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("DELETE FROM app_logs WHERE severity = 10;"); err != nil {
		t.Fatalf("DELETE non-PK table failed: %v", err)
	}
}

// TestGroup3_SelectInto_SyntheticRowID tests BUG-SELECT-INTO-PK-1:
// SELECT ... INTO with duplicate values or empty results does not guess Columns[0] as PK.
func TestGroup3_SelectInto_SyntheticRowID(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE items (id INT PRIMARY KEY, tag VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE items failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO items (id, tag) VALUES (1, 'electronics'), (2, 'electronics');"); err != nil {
		t.Fatalf("INSERT items failed: %v", err)
	}

	// 1. SELECT INTO with non-unique projection (tag has duplicates)
	if _, err := coord.ExecuteSQL("SELECT tag INTO tag_summary FROM items;"); err != nil {
		t.Fatalf("SELECT INTO tag_summary failed: %v", err)
	}

	// Subsequent insert of duplicate tag must succeed
	if _, err := coord.ExecuteSQL("INSERT INTO tag_summary (tag) VALUES ('electronics');"); err != nil {
		t.Fatalf("INSERT duplicate tag into tag_summary failed: %v", err)
	}

	// 2. SELECT INTO with 0 rows
	if _, err := coord.ExecuteSQL("SELECT tag INTO empty_tags FROM items WHERE id = 999;"); err != nil {
		t.Fatalf("SELECT INTO empty_tags failed: %v", err)
	}

	emptyTbl, err := eng.GetTable("empty_tags")
	if err != nil {
		t.Fatalf("GetTable empty_tags failed: %v", err)
	}
	tagCol, _, _ := emptyTbl.FindColumn("tag")
	if tagCol.PrimaryKey {
		t.Fatalf("expected tag column in empty_tags NOT to be primary key")
	}

	// Inserting duplicate rows into the 0-row materialized table must succeed
	if _, err := coord.ExecuteSQL("INSERT INTO empty_tags (tag) VALUES ('dup'), ('dup');"); err != nil {
		t.Fatalf("INSERT duplicates into empty_tags failed: %v", err)
	}
}

// TestGroup3_DerivedTable_EmptyAndDuplicates tests BUG-DERIVED-TABLE-PK-GUESS-EMPTY-1:
// Materialized derived tables with 0 rows or duplicates execute cleanly.
func TestGroup3_DerivedTable_EmptyAndDuplicates(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("CREATE TABLE users failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO users (id, name) VALUES (1, 'Alice'), (2, 'Alice');"); err != nil {
		t.Fatalf("INSERT users failed: %v", err)
	}

	// Derived table with duplicate names
	res, err := coord.ExecuteSQL("SELECT sub.name FROM (SELECT name FROM users) AS sub;")
	if err != nil {
		t.Fatalf("derived table query with duplicates failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got: %d", len(res.Rows))
	}

	// Derived table with 0 rows
	emptyRes, err := coord.ExecuteSQL("SELECT sub.name FROM (SELECT name FROM users WHERE id = 999) AS sub;")
	if err != nil {
		t.Fatalf("derived table query with 0 rows failed: %v", err)
	}
	if len(emptyRes.Rows) != 0 {
		t.Fatalf("expected 0 rows, got: %d", len(emptyRes.Rows))
	}
}

// TestGroup3_CTE_Materialize_DuplicatesAndTypes tests BUG-CTE-MATERIALIZE-1:
// CTE materialization does not force column 0 to be PK and preserves column types.
func TestGroup3_CTE_Materialize_DuplicatesAndTypes(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE depts (dept VARCHAR(50), budget INT);"); err != nil {
		t.Fatalf("CREATE TABLE depts failed: %v", err)
	}
	// Insert duplicate departments
	if _, err := coord.ExecuteSQL("INSERT INTO depts (dept, budget) VALUES ('Engineering', 100), ('Engineering', 200);"); err != nil {
		t.Fatalf("INSERT depts failed: %v", err)
	}

	// CTE query where column 0 has duplicate values ('Engineering')
	cteSQL := "WITH cte_depts AS (SELECT dept, budget FROM depts) SELECT cte_depts.dept, cte_depts.budget FROM cte_depts;"
	res, err := coord.ExecuteSQL(cteSQL)
	if err != nil {
		t.Fatalf("CTE execution with duplicate column 0 failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows from CTE, got: %d", len(res.Rows))
	}
}

// TestGroup3_DML_NonIdPK_NoGuessing tests BUG-DML-PK-RANDOM-1 & BUG-DML-PK-RANDOM-GUESS-1:
// Tables with a non-id PK (e.g. sku VARCHAR PRIMARY KEY) update and delete without guessing "id".
func TestGroup3_DML_NonIdPK_NoGuessing(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE products (sku VARCHAR(50) PRIMARY KEY, price INT);"); err != nil {
		t.Fatalf("CREATE TABLE products failed: %v", err)
	}
	if _, err := coord.ExecuteSQL("INSERT INTO products (sku, price) VALUES ('SKU-100', 50), ('SKU-200', 80);"); err != nil {
		t.Fatalf("INSERT products failed: %v", err)
	}

	// Update row by non-id PK
	if _, err := coord.ExecuteSQL("UPDATE products SET price = 99 WHERE sku = 'SKU-100';"); err != nil {
		t.Fatalf("UPDATE by non-id PK failed: %v", err)
	}

	// Verify update succeeded
	res, err := coord.ExecuteSQL("SELECT price FROM products WHERE sku = 'SKU-100';")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("SELECT after update failed: %v", err)
	}
	sRow := NewSQLRow(res.Rows[0])
	price, _ := sRow.GetInt("price")
	if price != 99 {
		t.Fatalf("expected price 99, got: %d", price)
	}

	// Delete row by non-id PK
	if _, err := coord.ExecuteSQL("DELETE FROM products WHERE sku = 'SKU-200';"); err != nil {
		t.Fatalf("DELETE by non-id PK failed: %v", err)
	}
	res2, err := coord.ExecuteSQL("SELECT * FROM products;")
	if err != nil || len(res2.Rows) != 1 {
		t.Fatalf("expected 1 row remaining, got: %d, err: %v", len(res2.Rows), err)
	}
}
