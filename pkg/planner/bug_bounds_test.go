package planner

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestRegression_BUG_BOUNDS_1_ParseIndexCondMultiPredicateDeterministic(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "account_id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "tenant_id", Type: storage.TypeInt},
			{Name: "status", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
		},
		Indexes: []storage.IndexSchema{
			{
				Name:    "idx_tenant",
				Columns: []string{"tenant_id"},
			},
		},
	}

	// AND condition containing both status and tenant_id
	cond := "status = 'ACTIVE' AND tenant_id = 42"

	// 1. With target index specified
	for i := 0; i < 20; i++ {
		low, high := ParseIndexCond(cond, schema, "idx_tenant")
		if low == nil || high == nil {
			t.Fatalf("run %d: expected non-nil bounds", i)
		}
		if low.Type != storage.TypeInt || low.IVal != 42 || high.IVal != 42 {
			t.Fatalf("run %d: expected tenant_id=42 bounds, got low=%+v high=%+v", i, low, high)
		}
	}

	// 2. Without target index specified, schema.Indexes priority selects indexed column tenant_id
	for i := 0; i < 20; i++ {
		low, high := ParseIndexCond(cond, schema)
		if low == nil || high == nil {
			t.Fatalf("run %d: expected non-nil bounds", i)
		}
		if low.Type != storage.TypeInt || low.IVal != 42 || high.IVal != 42 {
			t.Fatalf("run %d: expected tenant_id=42 bounds, got low=%+v high=%+v", i, low, high)
		}
	}
}
