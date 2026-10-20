package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestEvaluateSingleConditionMap_WhereInCaseInsensitive(t *testing.T) {
	rec := map[string]any{
		"UserID": 42,
		"Status": "ACTIVE",
	}

	// 1. Lowercase column name in WHERE IN
	if !evaluateSingleConditionMap(rec, "userid IN (10, 42, 99)") {
		t.Fatalf("expected true for userid IN (10, 42, 99)")
	}

	// 2. Uppercase column name with table prefix
	if !evaluateSingleConditionMap(rec, "u.USERID IN (42)") {
		t.Fatalf("expected true for u.USERID IN (42)")
	}

	// 3. Lowercase column name in NOT IN
	if evaluateSingleConditionMap(rec, "status NOT IN ('ACTIVE', 'PENDING')") {
		t.Fatalf("expected false for status NOT IN ('ACTIVE', 'PENDING')")
	}

	if !evaluateSingleConditionMap(rec, "status NOT IN ('INACTIVE', 'SUSPENDED')") {
		t.Fatalf("expected true for status NOT IN ('INACTIVE', 'SUSPENDED')")
	}
}

func TestWhereInCaseInsensitiveE2E(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	setupSQL := []string{
		"CREATE TABLE products (ProductID INT PRIMARY KEY, CategoryCode VARCHAR(20));",
		"INSERT INTO products (ProductID, CategoryCode) VALUES (1, 'ELEC');",
		"INSERT INTO products (ProductID, CategoryCode) VALUES (2, 'BOOK');",
	}

	for _, sql := range setupSQL {
		if _, err := coord.ExecuteSQL(sql); err != nil {
			t.Fatalf("failed setup sql %s: %v", sql, err)
		}
	}

	// Query with lowercase column names in WHERE IN
	res, err := coord.ExecuteSQL("SELECT productid FROM products WHERE categorycode IN ('ELEC', 'TOY');")
	if err != nil {
		t.Fatalf("SELECT with categorycode IN failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
}
