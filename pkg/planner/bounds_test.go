package planner

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestBounds_ParseWhereConditions(t *testing.T) {
	where := "age >= 18 AND age <= 65 AND status = 'active' AND score BETWEEN 80 AND 100 AND name = \"Alice\""
	eqs, ranges := ParseWhereConditions(where)

	if eqs["status"] != "active" {
		t.Fatalf("expected status='active', got %q", eqs["status"])
	}
	if eqs["name"] != "Alice" {
		t.Fatalf("expected name='Alice', got %q", eqs["name"])
	}

	ageRange, ok := ranges["age"]
	if !ok || !ageRange.HasMin || !ageRange.HasMax || ageRange.MinVal != "18" || ageRange.MaxVal != "65" {
		t.Fatalf("expected age in [18, 65], got %+v", ageRange)
	}

	scoreRange, ok := ranges["score"]
	if !ok || !scoreRange.HasMin || !scoreRange.HasMax || scoreRange.MinVal != "80" || scoreRange.MaxVal != "100" {
		t.Fatalf("expected score in [80, 100], got %+v", scoreRange)
	}
}

func TestBounds_ParseValueToType(t *testing.T) {
	tests := []struct {
		input    string
		dataType storage.DataType
		check    func(v storage.Value) bool
	}{
		{"12345", storage.TypeInt, func(v storage.Value) bool { return v.Type == storage.TypeInt && v.IVal == 12345 }},
		{"3.1415", storage.TypeFloat, func(v storage.Value) bool { return v.Type == storage.TypeFloat && v.FVal > 3.14 }},
		{"99.95", storage.TypeDecimal, func(v storage.Value) bool { return v.Type == storage.TypeDecimal && v.SVal == "99.95" }},
		{"true", storage.TypeBool, func(v storage.Value) bool { return v.Type == storage.TypeBool && v.BVal }},
		{"2026-10-16T09:00:00Z", storage.TypeDateTime, func(v storage.Value) bool { return v.Type == storage.TypeDateTime && v.TVal.Year() == 2026 }},
		{"2026-10-16", storage.TypeDate, func(v storage.Value) bool { return v.Type == storage.TypeDate && v.IVal > 0 }},
		{"09:30:00", storage.TypeTime, func(v storage.Value) bool { return v.Type == storage.TypeTime && v.IVal > 0 }},
		{"A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11", storage.TypeUUID, func(v storage.Value) bool { return v.Type == storage.TypeUUID && len(v.SVal) == 36 }},
		{"hello", storage.TypeVarChar, func(v storage.Value) bool { return v.Type == storage.TypeVarChar && v.SVal == "hello" }},
		{"NULL", storage.TypeInt, func(v storage.Value) bool { return v.IsNull }},
	}

	for _, tt := range tests {
		val := ParseValueToType(tt.input, tt.dataType)
		if !tt.check(val) {
			t.Errorf("ParseValueToType(%q, %v) failed verification, got: %+v", tt.input, tt.dataType, val)
		}
	}
}

func TestBounds_FindIndexAndBounds(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "tenant_id", Type: storage.TypeInt},
			{Name: "created_at", Type: storage.TypeDateTime},
			{Name: "email", Type: storage.TypeVarChar},
			{Name: "profile", Type: storage.TypeJSON},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_tenant_created",
				Columns: []string{"tenant_id", "created_at"},
			},
			{
				Name:    "idx_email",
				Columns: []string{"email"},
			},
			{
				Name:       "idx_profile_city",
				Columns:    []string{"profile"},
				ExprColumn: "profile",
				ExprPath:   "$.city",
			},
		},
	}

	// 1. Single-column equality
	idx, low, high, ok := FindIndexAndBounds(schema, "email = 'user@example.com'")
	if !ok || idx.Name != "idx_email" || low == nil || high == nil || low.SVal != "user@example.com" {
		t.Fatalf("expected idx_email match, got ok=%v, idx=%v, low=%v", ok, idx, low)
	}

	// 2. Composite index prefix match
	idx, low, high, ok = FindIndexAndBounds(schema, "tenant_id = 100")
	if !ok || idx.Name != "idx_tenant_created" || low == nil || high == nil {
		t.Fatalf("expected idx_tenant_created match, got ok=%v, idx=%v", ok, idx)
	}

	// 3. Functional expression match
	idx, low, high, ok = FindIndexAndBounds(schema, "profile->>'city' = 'Seoul'")
	if !ok || idx.Name != "idx_profile_city" || low == nil || low.SVal != "Seoul" {
		t.Fatalf("expected idx_profile_city match, got ok=%v, idx=%v, low=%v", ok, idx, low)
	}

	// 4. No matching index
	_, _, _, ok = FindIndexAndBounds(schema, "nonexistent_col = 'val'")
	if ok {
		t.Fatalf("expected ok=false for non-indexed col")
	}
}

func TestBounds_ParseIndexCond(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "order_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
			{Name: "doc", Type: storage.TypeJSON},
		},
	}

	// 1. Simple equality
	low, high := ParseIndexCond("(order_id = 500)", schema)
	if low == nil || high == nil || low.IVal != 500 || high.IVal != 500 {
		t.Fatalf("expected order_id = 500, got low=%+v, high=%+v", low, high)
	}

	// 2. Range condition
	low, high = ParseIndexCond("amount >= 10.5 AND amount <= 99.9", schema)
	if low == nil || high == nil || low.FVal != 10.5 || high.FVal != 99.9 {
		t.Fatalf("expected amount in [10.5, 99.9], got low=%+v, high=%+v", low, high)
	}

	// 3. Functional expression
	low, high = ParseIndexCond("doc->>'status' = 'shipped'", schema)
	if low == nil || high == nil || low.SVal != "shipped" {
		t.Fatalf("expected doc->>'status' = 'shipped', got low=%+v", low)
	}
}
