package sqlutil

import (
	"reflect"
	"testing"
)

type mockNullVal struct {
	null bool
}

func (m mockNullVal) IsNullValue() bool {
	return m.null
}

func TestDeduplicateRows(t *testing.T) {
	rows := []map[string]any{
		{"a": 1, "b": "x"},
		{"a": 2, "b": "y"},
		{"b": "x", "a": 1}, // duplicate with different key order
		{"a": 1, "b": "z"},
		{"a": nil, "b": "w"},
		{"a": mockNullVal{null: true}, "b": "w"}, // duplicate with mock NULL
	}

	dedup := DeduplicateRows(rows)
	if len(dedup) != 4 {
		t.Fatalf("expected 4 deduplicated rows, got %d: %+v", len(dedup), dedup)
	}

	expected := []map[string]any{
		{"a": 1, "b": "x"},
		{"a": 2, "b": "y"},
		{"a": 1, "b": "z"},
		{"a": nil, "b": "w"},
	}

	for i, exp := range expected {
		if !reflect.DeepEqual(dedup[i]["b"], exp["b"]) {
			t.Errorf("row %d 'b' mismatch: expected %v, got %v", i, exp["b"], dedup[i]["b"])
		}
	}
}

func TestDeduplicateRowsAndValues(t *testing.T) {
	rows := []map[string]any{
		{"c": "cat"},
		{"c": "dog"},
		{"c": "cat"},
		{"c": "bird"},
	}
	values := [][]any{
		{"cat"},
		{"dog"},
		{"cat"},
		{"bird"},
	}

	dRows, dVals := DeduplicateRowsAndValues(rows, values)
	if len(dRows) != 3 || len(dVals) != 3 {
		t.Fatalf("expected 3 items, got %d rows and %d values", len(dRows), len(dVals))
	}

	expectedVals := [][]any{
		{"cat"},
		{"dog"},
		{"bird"},
	}
	if !reflect.DeepEqual(dVals, expectedVals) {
		t.Errorf("expected values %+v, got %+v", expectedVals, dVals)
	}
}
