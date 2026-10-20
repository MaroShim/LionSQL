package engine

import (
	"reflect"
	"testing"

	"lionsql/pkg/storage"
)

func TestSQLRow_GetCaseInsensitiveAndAlias(t *testing.T) {
	row := NewSQLRow(map[string]any{
		"UserID":   42,
		"FullName": "Alice",
	}, []string{"UserID", "FullName"})

	// 1. Exact match
	v, ok := row.Get("UserID")
	if !ok || v != 42 {
		t.Fatalf("expected 42, got %v", v)
	}

	// 2. Lowercase match
	v, ok = row.Get("userid")
	if !ok || v != 42 {
		t.Fatalf("expected 42 via lowercase, got %v", v)
	}

	// 3. Table-prefixed match
	v, ok = row.Get("u.fullname")
	if !ok || v != "Alice" {
		t.Fatalf("expected 'Alice' via table prefix, got %v", v)
	}

	// 4. Case-insensitive Set
	row.Set("fullname", "Bob")
	if len(row.values) != 2 {
		t.Fatalf("Set must update in place without duplicate keys: %+v", row.values)
	}
	v, ok = row.Get("FullName")
	if !ok || v != "Bob" {
		t.Fatalf("expected 'Bob' after Set, got %v", v)
	}
}

func TestSQLRow_GetFirstDeterministic(t *testing.T) {
	row := NewSQLRow(map[string]any{
		"zebra": 99,
		"alpha": 1,
		"beta":  2,
	})

	// Run 50 iterations to ensure determinism across Go map seed
	for i := 0; i < 50; i++ {
		// When no cols provided and no explicit column order, sorted key 'alpha' is first
		v, ok := row.GetFirst()
		if !ok || v != 1 {
			t.Fatalf("run %d: expected deterministic first value 1 (alpha), got %v", i, v)
		}

		// When specific columns given, returns first matching
		v, ok = row.GetFirst("nonexistent", "BETA", "alpha")
		if !ok || v != 2 {
			t.Fatalf("run %d: expected 2 (beta), got %v", i, v)
		}
	}
}

func TestNormalizeRecordKeys(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "UserID", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "EmailAddress", Type: storage.TypeVarChar},
		},
	}

	input := storage.Record{
		"userid":        storage.IntValue(10),
		"emailaddress":  storage.StringValue("test@test.com"),
		"extra_col":     storage.IntValue(99),
	}

	normalized := NormalizeRecordKeys(schema, input)

	expectedKeys := []string{"EmailAddress", "UserID", "extra_col"}
	if !reflect.DeepEqual(normalized.SortedKeys(), expectedKeys) {
		t.Fatalf("expected normalized keys %+v, got %+v", expectedKeys, normalized.SortedKeys())
	}
}
