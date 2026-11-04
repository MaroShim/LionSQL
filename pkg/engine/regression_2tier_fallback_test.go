package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// TestRegression_2Tier_TablelessScalar_VDBEFallback ensures that complex table-less scalar
// expressions that fail VDBE compilation (e.g. nested aggregates, NULLIF, or intricate arithmetic)
// MUST gracefully fall back to the Volcano scalar execution engine without prematurely aborting.
//
// Invariant Contract:
// Tier-1 (VDBE) is an opportunistic fast-path. A compilation or execution failure in Tier-1
// MUST NOT return an error to the caller when a table-less scalar query can be evaluated by Tier-2.
func TestRegression_2Tier_TablelessScalar_VDBEFallback(t *testing.T) {
	eng := memengine.New()
	_ = eng.Open("")
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())

	queries := []struct {
		sql      string
		expected any
	}{
		{
			sql:      "SELECT ALL NULLIF ( - COUNT ( * ), + 67 * - - ( + 25 ) + 89 + - 39 * 63 ) + + 54 AS col2",
			expected: int64(53), // -1 + 54 = 53
		},
		{
			sql:      "SELECT + COUNT ( * ) AS col0",
			expected: int64(1),
		},
		{
			sql:      "SELECT ALL - ( + - 10 ) + COUNT ( 61 )",
			expected: int64(11), // 10 + 1 = 11
		},
		{
			sql:      "SELECT - COUNT ( - 25 )",
			expected: int64(-1),
		},
		{
			sql:      "SELECT 10 + 20",
			expected: int64(30),
		},
	}

	for _, tc := range queries {
		res, err := coord.ExecuteSQL(tc.sql)
		if err != nil {
			t.Fatalf("query failed unexpectedly: %q, err: %v", tc.sql, err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row for %q, got %d", tc.sql, len(res.Rows))
		}
		// Check that at least one column has the expected value
		matched := false
		for _, val := range res.Rows[0] {
			if val == tc.expected {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("expected value %v in row for query %q, got row: %v", tc.expected, tc.sql, res.Rows[0])
		}
	}
}
