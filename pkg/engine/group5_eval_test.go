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
