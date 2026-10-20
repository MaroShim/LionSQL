package storage

import (
	"reflect"
	"testing"
)

func TestRecord_GetAndSetCaseInsensitive(t *testing.T) {
	rec := Record{
		"UserID":    IntValue(100),
		"UserEmail": StringValue("user@example.com"),
	}

	// 1. Exact match
	v, ok := rec.Get("UserID")
	if !ok || v.IVal != 100 {
		t.Fatalf("expected 100, got %+v", v)
	}

	// 2. Lowercase lookup
	v, ok = rec.Get("userid")
	if !ok || v.IVal != 100 {
		t.Fatalf("expected 100 via lowercase lookup, got %+v", v)
	}

	// 3. Table-prefixed lookup
	v, ok = rec.Get("users.userid")
	if !ok || v.IVal != 100 {
		t.Fatalf("expected 100 via table-prefixed lookup, got %+v", v)
	}

	// 4. Set with lowercase key updates existing canonical key in place
	rec.Set("useremail", StringValue("updated@example.com"))
	if len(rec) != 2 {
		t.Fatalf("expected 2 keys, but Set created duplicate: %+v", rec)
	}
	if rec["UserEmail"].SVal != "updated@example.com" {
		t.Fatalf("expected UserEmail to be updated in place, got %+v", rec["UserEmail"])
	}

	// 5. Set new key
	rec.Set("Age", IntValue(30))
	if len(rec) != 3 {
		t.Fatalf("expected 3 keys after adding Age, got %d", len(rec))
	}
	v, ok = rec.Get("age")
	if !ok || v.IVal != 30 {
		t.Fatalf("expected Age 30 via lowercase, got %+v", v)
	}
}

func TestRecord_FirstValueDeterministic(t *testing.T) {
	rec := Record{
		"zebra": IntValue(99),
		"alpha": IntValue(1),
		"beta":  IntValue(2),
	}

	// Run 50 iterations to ensure determinism across Go map hash seed
	for i := 0; i < 50; i++ {
		// When no cols specified, must always return 'alpha' (first in sorted order)
		v, ok := rec.FirstValue()
		if !ok || v.IVal != 1 {
			t.Fatalf("run %d: expected deterministic first value 1 (alpha), got %+v", i, v)
		}

		// When specific cols given, must return first matched column
		v, ok = rec.FirstValue("nonexistent", "BETA", "alpha")
		if !ok || v.IVal != 2 {
			t.Fatalf("run %d: expected 2 (beta), got %+v", i, v)
		}
	}
}

func TestRecord_SortedKeysAndClone(t *testing.T) {
	rec := Record{
		"colZ": IntValue(3),
		"colA": IntValue(1),
		"colM": IntValue(2),
	}

	keys := rec.SortedKeys()
	expected := []string{"colA", "colM", "colZ"}
	if !reflect.DeepEqual(keys, expected) {
		t.Fatalf("expected sorted keys %+v, got %+v", expected, keys)
	}

	cloned := rec.Clone()
	if !reflect.DeepEqual(rec, cloned) {
		t.Fatalf("cloned record does not match original: %+v vs %+v", cloned, rec)
	}

	cloned.Set("colA", IntValue(999))
	if rec["colA"].IVal != 1 {
		t.Fatalf("modifying clone must not mutate original record")
	}
}

func TestRecord_Delete(t *testing.T) {
	rec := Record{
		"TargetCol": IntValue(42),
	}

	if !rec.Delete("targetcol") {
		t.Fatalf("expected Delete to return true for case-insensitive match")
	}
	if len(rec) != 0 {
		t.Fatalf("expected empty record after delete, got %+v", rec)
	}
}
