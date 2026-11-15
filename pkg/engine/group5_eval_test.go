package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestGroup5_ValueCompareAndEqual(t *testing.T) {
	// 1. Int vs Float Compare
	vInt := storage.IntValue(100)
	vFloat := storage.FloatValue(200.5)
	if cmp := vInt.Compare(vFloat); cmp >= 0 {
		t.Errorf("expected 100 < 200.5, got cmp=%d", cmp)
	}
	if cmp := vFloat.Compare(vInt); cmp <= 0 {
		t.Errorf("expected 200.5 > 100, got cmp=%d", cmp)
	}

	vFloatEqual := storage.FloatValue(100.0)
	if cmp := vInt.Compare(vFloatEqual); cmp != 0 {
		t.Errorf("expected 100 == 100.0, got cmp=%d", cmp)
	}
	if !vInt.Equal(vFloatEqual) || !vFloatEqual.Equal(vInt) {
		t.Errorf("expected vInt.Equal(vFloatEqual) to be true")
	}

	// 2. Float precision: distinct past 6th decimal place must not be equal
	f1 := storage.FloatValue(1.1234567)
	f2 := storage.FloatValue(1.1234568)
	if f1.Equal(f2) {
		t.Errorf("1.1234567 should not equal 1.1234568")
	}

	// 3. DateTime vs VarChar Compare
	dt := storage.DateTimeValue(time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC))
	strBefore := storage.StringValue("2024-05-31 23:59:59")
	strAfter := storage.StringValue("2024-06-02 00:00:00")

	if cmp := dt.Compare(strBefore); cmp <= 0 {
		t.Errorf("expected 2024-06-01 > 2024-05-31, got cmp=%d", cmp)
	}
	if cmp := dt.Compare(strAfter); cmp >= 0 {
		t.Errorf("expected 2024-06-01 < 2024-06-02, got cmp=%d", cmp)
	}

	// 4. Date vs VarChar Compare
	dateVal := storage.DateValueFromString("2024-06-01")
	strDateSame := storage.StringValue("2024-06-01")
	if cmp := dateVal.Compare(strDateSame); cmp != 0 {
		t.Errorf("expected 2024-06-01 == 2024-06-01, got cmp=%d", cmp)
	}
	if !dateVal.Equal(strDateSame) {
		t.Errorf("expected DateValue Equal StringValue on identical date")
	}
}

func TestGroup5_EvalUpdateExpr_Advanced(t *testing.T) {
	rec := storage.Record{
		"Price":    storage.IntValue(100),
		"Tax":      storage.IntValue(10),
		"Rate":     storage.FloatValue(1.2),
		"Subtotal": storage.DecimalValue("45.50"),
	}

	// 1. Bracketed column name
	v1, err1 := evalUpdateExpr("[Price] + 15", rec, storage.TypeInt)
	if err1 != nil || v1.IVal != 115 {
		t.Fatalf("expected [Price] + 15 = 115, got %+v (err: %v)", v1, err1)
	}

	// 2. Constant leading
	v2, err2 := evalUpdateExpr("25 + [Price]", rec, storage.TypeInt)
	if err2 != nil || v2.IVal != 125 {
		t.Fatalf("expected 25 + [Price] = 125, got %+v (err: %v)", v2, err2)
	}

	// 3. Column to column arithmetic
	v3, err3 := evalUpdateExpr("[Price] + [Tax]", rec, storage.TypeInt)
	if err3 != nil || v3.IVal != 110 {
		t.Fatalf("expected [Price] + [Tax] = 110, got %+v (err: %v)", v3, err3)
	}

	// 4. Divide by zero must return explicit error
	_, err4 := evalUpdateExpr("[Price] / 0", rec, storage.TypeInt)
	if err4 == nil || !strings.Contains(err4.Error(), "divide by zero") {
		t.Fatalf("expected divide by zero error, got: %v", err4)
	}

	// 5. Normal division
	v5, err5 := evalUpdateExpr("[Price] / 2", rec, storage.TypeInt)
	if err5 != nil || v5.IVal != 50 {
		t.Fatalf("expected [Price] / 2 = 50, got %+v (err: %v)", v5, err5)
	}
}

func TestGroup5_UpdateDivideByZero_DML_Rollback(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE products (id INT PRIMARY KEY, qty INT);")
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO products (id, qty) VALUES (1, 10);")
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Executing update with divide by zero must fail with error
	_, err = coord.ExecuteSQL("UPDATE products SET qty = qty / 0 WHERE id = 1;")
	if err == nil {
		t.Fatalf("expected divide by zero error from UPDATE, but got success")
	}

	// Value must remain unmodified
	res, err := coord.ExecuteSQL("SELECT qty FROM products WHERE id = 1;")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("select failed: %v", err)
	}
	if res.Rows[0]["qty"] != int64(10) && res.Rows[0]["qty"] != 10 {
		t.Fatalf("expected qty to remain 10 after failed update, got: %v", res.Rows[0]["qty"])
	}
}

func TestGroup5_Aggregate_MixedDecimalIntSum(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE sales (id INT PRIMARY KEY, grp INT, amount DECIMAL(10, 2));")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	// Insert integer and decimal values
	_, err = coord.ExecuteSQL("INSERT INTO sales (id, grp, amount) VALUES (1, 1, 100);")
	if err != nil {
		t.Fatalf("insert 1 failed: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO sales (id, grp, amount) VALUES (2, 1, 50.25);")
	if err != nil {
		t.Fatalf("insert 2 failed: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO sales (id, grp, amount) VALUES (3, 1, 25);")
	if err != nil {
		t.Fatalf("insert 3 failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT SUM(amount) AS total FROM sales GROUP BY grp;")
	if err != nil {
		t.Fatalf("sum query failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}

	val := res.Rows[0]["total"]
	valStr := strings.TrimSpace(strings.TrimRight(strings.TrimRight(fmt.Sprintf("%v", val), "0"), "."))
	if !strings.Contains(valStr, "175.25") && valStr != "175.25" {
		t.Fatalf("expected total 175.25, got %v (%s)", val, valStr)
	}
}

func TestGroup5_API_DecimalAndDateTimeSerialization(t *testing.T) {
	decVal := storage.DecimalValue("12345.6789")
	if decVal.String() != "12345.6789" {
		t.Fatalf("unexpected decimal string: %s", decVal.String())
	}

	num := json.Number(decVal.String())
	b, err := json.Marshal(map[string]any{"balance": num})
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	if !strings.Contains(string(b), `"balance":12345.6789`) {
		t.Fatalf("expected unquoted exact numeric json representation, got: %s", string(b))
	}

	now := time.Date(2024, 10, 20, 15, 30, 0, 0, time.UTC)
	dtVal := storage.DateTimeValue(now)
	if dtVal.SVal == "" {
		t.Fatalf("DateTimeValue SVal should not be empty")
	}
}

func TestParseInCondition_Regression(t *testing.T) {
	tests := []struct {
		input       string
		wantCol     string
		wantIsNot   bool
		wantValList string
		wantOk      bool
	}{
		// Valid single IN conditions
		{
			input:       "status IN ('active', 'pending')",
			wantCol:     "status",
			wantIsNot:   false,
			wantValList: "'active', 'pending'",
			wantOk:      true,
		},
		{
			input:       "[users].[role] NOT IN ('admin', 'superuser')",
			wantCol:     "users.role",
			wantIsNot:   true,
			wantValList: "'admin', 'superuser'",
			wantOk:      true,
		},
		{
			input:       "id IN (1, 2, 3)",
			wantCol:     "id",
			wantIsNot:   false,
			wantValList: "1, 2, 3",
			wantOk:      true,
		},

		// Compound conditions that must NOT be parsed as a single IN clause
		{
			input:  "col1 IN (1, 2) AND (col2 = 3)",
			wantOk: false,
		},
		{
			input:  "a > 10 AND b IN (1, 2)",
			wantOk: false,
		},
		{
			input:  "(col1 IN (1, 2)) OR (col2 IN (3, 4))",
			wantOk: false,
		},
		{
			input:  "col1 IN (SELECT id FROM t) AND col2 > 5",
			wantOk: false,
		},
		{
			input:  "x + y IN (1, 2)",
			wantOk: false,
		},
	}

	for _, tc := range tests {
		col, isNot, valList, ok := parseInCondition(tc.input)
		if ok != tc.wantOk {
			t.Errorf("parseInCondition(%q): got ok=%v, want %v", tc.input, ok, tc.wantOk)
			continue
		}
		if tc.wantOk {
			if col != tc.wantCol {
				t.Errorf("parseInCondition(%q): got col=%q, want %q", tc.input, col, tc.wantCol)
			}
			if isNot != tc.wantIsNot {
				t.Errorf("parseInCondition(%q): got isNot=%v, want %v", tc.input, isNot, tc.wantIsNot)
			}
			if valList != tc.wantValList {
				t.Errorf("parseInCondition(%q): got valList=%q, want %q", tc.input, valList, tc.wantValList)
			}
		}
	}
}

// TestEval_NotIn_Null_ThreeValuedLogic verifies that NOT IN predicates containing NULL
// strictly adhere to SQL-92 Three-Valued Logic (3VL UNKNOWN) and evaluate to false in WHERE clauses.
func TestEval_NotIn_Null_ThreeValuedLogic(t *testing.T) {
	rec1 := map[string]any{"val": 30, "status": "ACTIVE"}
	rec2 := map[string]any{"val": 10, "status": "INACTIVE"}
	recNull := map[string]any{"val": nil, "status": nil}

	// 1. evaluateSingleConditionMap direct unit tests
	// val=30 NOT IN (10, 20, NULL): 30 is not 10 or 20, but NULL is present -> UNKNOWN -> false
	if evaluateSingleConditionMap(rec1, "val NOT IN (10, 20, NULL)") {
		t.Fatalf("expected false for val NOT IN (10, 20, NULL) when val=30, got true")
	}

	// status='ACTIVE' NOT IN ('INACTIVE', NULL): ACTIVE is not INACTIVE, but NULL is present -> UNKNOWN -> false
	if evaluateSingleConditionMap(rec1, "status NOT IN ('INACTIVE', NULL)") {
		t.Fatalf("expected false for status NOT IN ('INACTIVE', NULL) when status='ACTIVE', got true")
	}

	// val=10 NOT IN (10, 20, NULL): 10 matches -> FALSE
	if evaluateSingleConditionMap(rec2, "val NOT IN (10, 20, NULL)") {
		t.Fatalf("expected false for val NOT IN (10, 20, NULL) when val=10, got true")
	}

	// val=30 NOT IN (10, 20): no NULL present, 30 not in (10, 20) -> TRUE
	if !evaluateSingleConditionMap(rec1, "val NOT IN (10, 20)") {
		t.Fatalf("expected true for val NOT IN (10, 20) when val=30, got false")
	}

	// val=NULL NOT IN (10, 20): LHS is NULL -> UNKNOWN -> false
	if evaluateSingleConditionMap(recNull, "val NOT IN (10, 20)") {
		t.Fatalf("expected false for val NOT IN (10, 20) when val is NULL, got true")
	}

	// 2. evaluateWhereCondition integration
	sRec1 := storage.Record{"val": storage.IntValue(30)}
	if evaluateWhereCondition(sRec1, "val NOT IN (10, 20, NULL)") {
		t.Fatalf("expected evaluateWhereCondition to return false for NOT IN with NULL")
	}
	if !evaluateWhereCondition(sRec1, "val NOT IN (10, 20)") {
		t.Fatalf("expected evaluateWhereCondition to return true for NOT IN without NULL")
	}
}

func TestCoordinator_Query_NotIn_Null_ThreeValuedLogic(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	_, err := coord.ExecuteSQL(`CREATE TABLE t_3vl (id INT PRIMARY KEY, num INT);`)
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}

	_, err = coord.ExecuteSQL(`INSERT INTO t_3vl VALUES (1, 10), (2, 20), (3, 30);`)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 1. SELECT WHERE num NOT IN (20, NULL) -> must return 0 rows
	// (20 is matched -> false; 10 and 30 do not match 20, but with NULL -> UNKNOWN -> filtered out)
	res, err := coord.ExecuteSQL(`SELECT * FROM t_3vl WHERE num NOT IN (20, NULL);`)
	if err != nil {
		t.Fatalf("select with NOT IN NULL failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows for 'num NOT IN (20, NULL)', got %d rows: %+v", len(res.Rows), res.Rows)
	}

	// 2. SELECT WHERE num NOT IN (40, 50) -> must return all 3 rows (10, 20, 30)
	res, err = coord.ExecuteSQL(`SELECT * FROM t_3vl WHERE num NOT IN (40, 50);`)
	if err != nil {
		t.Fatalf("select with NOT IN without NULL failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows for 'num NOT IN (40, 50)', got %d rows", len(res.Rows))
	}

	// 3. Subquery containing NULL in NOT IN:
	// CREATE TABLE t_nulls (val INT);
	// INSERT INTO t_nulls VALUES (20), (NULL);
	// SELECT * FROM t_3vl WHERE num NOT IN (SELECT val FROM t_nulls); -> 0 rows
	_, err = coord.ExecuteSQL(`CREATE TABLE t_nulls (id INT PRIMARY KEY, val INT);`)
	if err != nil {
		t.Fatalf("create t_nulls failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`INSERT INTO t_nulls VALUES (1, 20), (2, NULL);`)
	if err != nil {
		t.Fatalf("insert t_nulls failed: %v", err)
	}
	res, err = coord.ExecuteSQL(`SELECT * FROM t_3vl WHERE num NOT IN (SELECT val FROM t_nulls);`)
	if err != nil {
		t.Fatalf("subquery select failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows for subquery containing NULL, got %d rows: %+v", len(res.Rows), res.Rows)
	}
}


