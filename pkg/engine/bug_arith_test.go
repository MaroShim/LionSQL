package engine

import (
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func TestEvalUpdateExprCaseInsensitivity(t *testing.T) {
	rec := storage.Record{
		"Price": storage.IntValue(100),
		"Rate":  storage.FloatValue(1.5),
	}

	// 1. Lowercase reference to uppercase column
	v1 := evalUpdateExpr("price + 20", rec, storage.TypeInt)
	if v1.IsNull || v1.IVal != 120 {
		t.Fatalf("expected 120, got %+v", v1)
	}

	// 2. Uppercase reference to lowercase column
	rec2 := storage.Record{
		"price": storage.IntValue(50),
	}
	v2 := evalUpdateExpr("PRICE * 3", rec2, storage.TypeInt)
	if v2.IsNull || v2.IVal != 150 {
		t.Fatalf("expected 150, got %+v", v2)
	}

	// 3. Float arithmetic with mixed case
	v3 := evalUpdateExpr("rate + 0.5", rec, storage.TypeFloat)
	if v3.IsNull || v3.FVal != 2.0 {
		t.Fatalf("expected 2.0, got %+v", v3)
	}
}

func TestUpdateArithmeticCaseInsensitiveDML(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	setupSQL := []string{
		"CREATE TABLE items (id INT PRIMARY KEY, Price INT);",
		"INSERT INTO items (id, Price) VALUES (1, 100);",
		"UPDATE items SET price = price + 15 OUTPUT INSERTED.Price WHERE id = 1;",
	}

	for _, sql := range setupSQL {
		res, err := coord.ExecuteSQL(sql)
		if err != nil {
			t.Fatalf("failed executing %s: %v", sql, err)
		}
		_ = res
	}

	selRes, err := coord.ExecuteSQL("SELECT Price FROM items WHERE id = 1;")
	if err != nil {
		t.Fatalf("failed select: %v", err)
	}
	if len(selRes.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(selRes.Rows))
	}
	val, ok := selRes.Rows[0]["Price"]
	if !ok {
		val = selRes.Rows[0]["price"]
	}
	if val != int64(115) && val != 115 {
		t.Fatalf("expected Price 115, got %v (%T)", val, val)
	}
}
