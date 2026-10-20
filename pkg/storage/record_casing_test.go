package storage

import (
	"testing"
)

func TestRecordCasingAndBrackets(t *testing.T) {
	rec := make(Record)

	// Set with bracketed key
	rec.Set("[Price]", FloatValue(49.99))
	rec.Set("User.Name", StringValue("Bob"))

	// 1. Get with casing and stripped table prefix
	v, ok := rec.Get("price")
	if !ok || v.FVal != 49.99 {
		t.Errorf("Get('price') failed, got %v", v)
	}

	v2, ok := rec.Get("[PRICE]")
	if !ok || v2.FVal != 49.99 {
		t.Errorf("Get('[PRICE]') failed, got %v", v2)
	}

	v3, ok := rec.Get("name")
	if !ok || v3.SVal != "Bob" {
		t.Errorf("Get('name') failed for table prefix, got %v", v3)
	}

	// 2. Set updates existing case-variant key
	rec.Set("price", FloatValue(59.99))
	if len(rec) != 2 {
		t.Errorf("expected 2 keys, got %d", len(rec))
	}
	if v, _ := rec.Get("price"); v.FVal != 59.99 {
		t.Errorf("expected updated price 59.99, got %v", v)
	}

	// 3. Delete case-insensitively
	if !rec.Delete("[price]") {
		t.Errorf("Delete('[price]') failed")
	}
	if _, ok := rec.Get("price"); ok {
		t.Errorf("price should be deleted")
	}

	// 4. Schema RecordToRow with case mismatch
	schema := &TableSchema{
		Name: "Products",
		Columns: []Column{
			{Name: "ProductName", Type: TypeVarChar},
		},
	}
	rowRec := Record{
		"productname": StringValue("Gadget"),
	}
	row := schema.RecordToRow(rowRec)
	if len(row.Values) != 1 || row.Values[0].IsNull || row.Values[0].SVal != "Gadget" {
		t.Errorf("RecordToRow failed with casing mismatch, got %v", row.Values)
	}
}
