package api

import (
	"net/url"
	"testing"

	"sqlion/pkg/storage"
)

func TestRecordsFilter_ParseAndEval(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
			{Name: "status", Type: storage.TypeVarChar},
			{Name: "is_active", Type: storage.TypeBool},
			{Name: "notes", Type: storage.TypeVarChar, Nullable: true},
		},
	}

	row1 := map[string]any{
		"id":        int64(1),
		"holder":    "Alice Smith",
		"balance":   float64(1000.50),
		"status":    "active",
		"is_active": true,
		"notes":     nil,
	}

	row2 := map[string]any{
		"id":        int64(2),
		"holder":    "Bob Jones",
		"balance":   float64(300.00),
		"status":    "pending",
		"is_active": false,
		"notes":     "Important VIP",
	}

	tests := []struct {
		filter string
		row1   bool
		row2   bool
	}{
		{"balance > 500", true, false},
		{"balance >= 1000.5", true, false},
		{"balance <= 300", false, true},
		{"balance < 500", false, true},
		{"status = 'active'", true, false},
		{"status = active", true, false},
		{"status != 'active'", false, true},
		{"holder ~ 'smith'", true, false},
		{"holder ~ 'jones'", false, true},
		{"holder LIKE '%Smith%'", true, false},
		{"is_active = true", true, false},
		{"is_active = false", false, true},
		{"notes IS NULL", true, false},
		{"notes IS NOT NULL", false, true},
		{"balance > 500 && status = 'active'", true, false},
		{"balance > 500, status = active", true, false},
		{"balance > 500 || balance = 300", true, true},
		{"(balance > 500 && status = 'active') || status = 'pending'", true, true},
		{"balance > 500 && status = 'pending'", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			expr, err := ParseFilter(tt.filter)
			if err != nil {
				t.Fatalf("failed to parse filter %q: %v", tt.filter, err)
			}
			m1 := expr.Matches(row1, schema)
			if m1 != tt.row1 {
				t.Errorf("filter %q on row1: expected %v, got %v", tt.filter, tt.row1, m1)
			}
			m2 := expr.Matches(row2, schema)
			if m2 != tt.row2 {
				t.Errorf("filter %q on row2: expected %v, got %v", tt.filter, tt.row2, m2)
			}
		})
	}
}

func TestRecordsFilter_QueryParams(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "age", Type: storage.TypeInt},
		},
	}

	q := url.Values{}
	q.Set("filter", "age >= 18")
	q.Set("name~", "Ali")

	expr, err := ParseQueryParamsFilter(q, schema)
	if err != nil {
		t.Fatalf("ParseQueryParamsFilter failed: %v", err)
	}

	rowPass := map[string]any{"id": 1, "name": "Alice", "age": 25}
	rowFailAge := map[string]any{"id": 2, "name": "Alice Jr", "age": 10}
	rowFailName := map[string]any{"id": 3, "name": "Bob", "age": 30}

	if !expr.Matches(rowPass, schema) {
		t.Errorf("expected rowPass to match")
	}
	if expr.Matches(rowFailAge, schema) {
		t.Errorf("expected rowFailAge not to match")
	}
	if expr.Matches(rowFailName, schema) {
		t.Errorf("expected rowFailName not to match")
	}
}
