package engine

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/execution"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestGroup4_SplitValues_WithCommaInFunction(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// Create table
	if _, err := coord.ExecuteSQL("CREATE TABLE test_vals (id INT PRIMARY KEY, note VARCHAR(100), num INT)"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	// Insert row where value has comma inside function / string
	if _, err := coord.ExecuteSQL("INSERT INTO test_vals (id, note, num) VALUES (1, COALESCE(NULL, 'fallback, comma'), 42)"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT id, note, num FROM test_vals WHERE id = 1")
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	noteVal := fmt.Sprintf("%v", res.Rows[0]["note"])
	if !strings.Contains(noteVal, "fallback, comma") {
		t.Fatalf("unexpected note value: %v", noteVal)
	}
	numVal := fmt.Sprintf("%v", res.Rows[0]["num"])
	if numVal != "42" {
		t.Fatalf("unexpected num value: %v", numVal)
	}
}

func TestGroup4_UpdateSet_CommaInStringsAndFunctions(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE test_upd (id INT PRIMARY KEY, name VARCHAR(100), age INT)"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("INSERT INTO test_upd (id, name, age) VALUES (1, 'Initial', 20)"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Update with comma in string literal and multiple columns
	if _, err := coord.ExecuteSQL("UPDATE test_upd SET name = 'Seoul, Korea', age = 25 WHERE id = 1"); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT name, age FROM test_upd WHERE id = 1")
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	nameVal := fmt.Sprintf("%v", res.Rows[0]["name"])
	if nameVal != "Seoul, Korea" {
		t.Fatalf("expected 'Seoul, Korea', got %v", nameVal)
	}
	ageVal := fmt.Sprintf("%v", res.Rows[0]["age"])
	if ageVal != "25" {
		t.Fatalf("expected age 25, got %v", ageVal)
	}
}

func TestGroup4_WhereIn_CommaInStringLiteral(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE test_in (id INT PRIMARY KEY, city VARCHAR(100))"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("INSERT INTO test_in (id, city) VALUES (1, 'Seoul, Korea'), (2, 'Tokyo, Japan'), (3, 'New York, USA')"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// WHERE city IN ('Seoul, Korea', 'London, UK')
	res, err := coord.ExecuteSQL("SELECT id, city FROM test_in WHERE city IN ('Seoul, Korea', 'London, UK')")
	if err != nil {
		t.Fatalf("select with IN failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for 'Seoul, Korea', got %d", len(res.Rows))
	}
	cityVal := fmt.Sprintf("%v", res.Rows[0]["city"])
	if cityVal != "Seoul, Korea" {
		t.Fatalf("expected 'Seoul, Korea', got %v", cityVal)
	}
}

func TestGroup4_OrderBy_MultipleColumnsAndDesc(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE test_order (id INT PRIMARY KEY, dept INT, score INT)"); err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	if _, err := coord.ExecuteSQL("INSERT INTO test_order (id, dept, score) VALUES (1, 10, 50), (2, 10, 90), (3, 20, 80), (4, 20, 95)"); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// ORDER BY dept ASC, score DESC
	res, err := coord.ExecuteSQL("SELECT id, dept, score FROM test_order ORDER BY dept ASC, score DESC")
	if err != nil {
		t.Fatalf("select with multi ORDER BY failed: %v", err)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res.Rows))
	}

	// First row should be dept 10, score 90 (id 2)
	id0 := fmt.Sprintf("%v", res.Rows[0]["id"])
	if id0 != "2" {
		t.Fatalf("expected first row id 2, got %v", id0)
	}
	// Second row should be dept 10, score 50 (id 1)
	id1 := fmt.Sprintf("%v", res.Rows[1]["id"])
	if id1 != "1" {
		t.Fatalf("expected second row id 1, got %v", id1)
	}
	// Third row should be dept 20, score 95 (id 4)
	id2 := fmt.Sprintf("%v", res.Rows[2]["id"])
	if id2 != "4" {
		t.Fatalf("expected third row id 4, got %v", id2)
	}
}

func TestGroup4_DDL_SplitQuoteDefault(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// Column with DEFAULT containing comma inside quotes
	if _, err := coord.ExecuteSQL("CREATE TABLE test_ddl_def (id INT PRIMARY KEY, status VARCHAR(50) DEFAULT 'draft, pending', count INT)"); err != nil {
		t.Fatalf("create table with quoted comma default failed: %v", err)
	}

	tbl, err := eng.GetTable("test_ddl_def")
	if err != nil {
		t.Fatalf("get table failed: %v", err)
	}
	if len(tbl.Columns) < 3 {
		t.Fatalf("expected at least 3 columns, got %d", len(tbl.Columns))
	}

	// Verify column names are id, status, count (not split into 'draft')
	statusCol, _, foundStatus := tbl.FindColumn("status")
	if !foundStatus {
		t.Fatalf("status column not found")
	}
	if !strings.Contains(statusCol.Default, "draft, pending") {
		t.Fatalf("expected default containing 'draft, pending', got %q", statusCol.Default)
	}
	_, _, foundCount := tbl.FindColumn("count")
	if !foundCount {
		t.Fatalf("count column not found")
	}
}

func TestGroup4_DDL_CompositePrimaryKey(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE test_comp_pk (order_id INT, item_id INT, qty INT, PRIMARY KEY (order_id, item_id))"); err != nil {
		t.Fatalf("create table with composite PK failed: %v", err)
	}

	tbl, err := eng.GetTable("test_comp_pk")
	if err != nil {
		t.Fatalf("get table failed: %v", err)
	}

	c1, _, _ := tbl.FindColumn("order_id")
	c2, _, _ := tbl.FindColumn("item_id")
	c3, _, _ := tbl.FindColumn("qty")

	if !c1.PrimaryKey {
		t.Fatalf("expected order_id to be PrimaryKey")
	}
	if !c2.PrimaryKey {
		t.Fatalf("expected item_id to be PrimaryKey")
	}
	if c3.PrimaryKey {
		t.Fatalf("qty should NOT be PrimaryKey")
	}
}

func TestGroup4_SpParamDeclarations_Decimal(t *testing.T) {
	declStr := "@p1 INT, @price DECIMAL(10, 2), @name VARCHAR(50)"
	decls := parseSpParamDeclarations(declStr)
	if len(decls) != 3 {
		t.Fatalf("expected 3 declarations, got %d: %#v", len(decls), decls)
	}

	if decls[0].Name != "@p1" || decls[0].Type != "INT" {
		t.Fatalf("unexpected p1: %#v", decls[0])
	}
	if decls[1].Name != "@price" || decls[1].Type != "DECIMAL(10, 2)" {
		t.Fatalf("unexpected p2: %#v", decls[1])
	}
	if decls[2].Name != "@name" || decls[2].Type != "VARCHAR(50)" {
		t.Fatalf("unexpected p3: %#v", decls[2])
	}
}

func TestGroup4_Project_CastAsAlias(t *testing.T) {
	// Test that ProjectionIterator doesn't truncate CAST(col AS type)
	childSchema := &storage.TableSchema{
		Name: "t",
		Columns: []storage.Column{
			{Name: "score", Type: storage.TypeInt},
		},
	}
	childIter := execution.NewSeqScanIterator(nil, "t", "t", childSchema)
	proj := execution.NewProjectionIterator(childIter, []string{"CAST(score AS VARCHAR(20)) AS score_str", "CAST(score AS INT)"})

	schema := proj.Schema()
	if len(schema) != 2 {
		t.Fatalf("expected 2 schema columns, got %d: %v", len(schema), schema)
	}
	if schema[0] != "score_str" {
		t.Fatalf("expected alias 'score_str', got %q", schema[0])
	}
	if !strings.EqualFold(schema[1], "CAST(score AS INT)") {
		t.Fatalf("expected alias 'CAST(score AS INT)', got %q", schema[1])
	}
}

func TestGroup4_Planner_DecorrelateEscapedQuotes(t *testing.T) {
	where := "o.name = 'O''Reilly AND Sons' AND o.user_id = u.id AND [col AND bar] = 1"
	conds := sqlutil.SplitAndConditions(where)
	if len(conds) != 3 {
		t.Fatalf("expected 3 conditions, got %d: %#v", len(conds), conds)
	}
	if conds[0] != "o.name = 'O''Reilly AND Sons'" {
		t.Fatalf("expected cond[0] to be uncorrupted quote, got %q", conds[0])
	}
	if conds[1] != "o.user_id = u.id" {
		t.Fatalf("expected cond[1] to be join condition, got %q", conds[1])
	}
	if conds[2] != "[col AND bar] = 1" {
		t.Fatalf("expected cond[2] to preserve brackets, got %q", conds[2])
	}
}

func TestGroup4_Catalog_MultiColumnOrderBy(t *testing.T) {
	rows := []map[string]any{
		{"name": "b_table", "type": "USER_TABLE", "obj_id": 2},
		{"name": "a_table", "type": "USER_TABLE", "obj_id": 1},
		{"name": "z_view", "type": "VIEW", "obj_id": 3},
	}

	res, err := virtual.FormatResult([]string{"name", "type", "obj_id"}, rows, "*", "", "type ASC, name DESC", -1)
	if err != nil {
		t.Fatalf("format result failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	// type USER_TABLE (a_table, b_table) -> name DESC: b_table first, then a_table
	first := res.Rows[0]["name"]
	second := res.Rows[1]["name"]
	if first != "b_table" || second != "a_table" {
		t.Fatalf("expected b_table then a_table, got %v, %v", first, second)
	}
}

func TestGroup4_MergeSet_CommaInStringsAndFunctions(t *testing.T) {
	pairs := parseSetPairs("name = 'Doe, John', score = COALESCE(src.score, 0), note = 'a, b, c'")
	if len(pairs) != 3 {
		t.Fatalf("expected 3 set pairs, got %d: %#v", len(pairs), pairs)
	}
	if pairs[0].column != "name" || pairs[0].expr != "'Doe, John'" {
		t.Fatalf("unexpected pair 0: %#v", pairs[0])
	}
	if pairs[1].column != "score" || pairs[1].expr != "COALESCE(src.score, 0)" {
		t.Fatalf("unexpected pair 1: %#v", pairs[1])
	}
	if pairs[2].column != "note" || pairs[2].expr != "'a, b, c'" {
		t.Fatalf("unexpected pair 2: %#v", pairs[2])
	}
}

func TestGroup4_JoinParse_MultiCond(t *testing.T) {
	cond := "a.id = b.id AND a.tenant_id = b.tenant_id"
	l, r := execution.ParseJoinCond(cond)
	if l != "a.id" || r != "b.id" {
		t.Fatalf("expected a.id = b.id, got %q = %q", l, r)
	}
}

