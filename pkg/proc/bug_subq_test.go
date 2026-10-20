package proc

import (
	"testing"
)

func TestRegression_BUG_SUBQ_1_ScalarSubqueryCaseInsensitiveCols(t *testing.T) {
	// Subquery returning rows where column name in firstRow has different case from cols[0]
	// and multiple columns exist in firstRow to catch random map iteration.
	mockExec := func(query string) ([]map[string]any, []string, error) {
		return []map[string]any{
			{
				"OTHER_COL": 99999,
				"PRICE":     150,
				"EXTRA_COL": "garbage",
			},
		}, []string{"price", "other_col", "extra_col"}, nil
	}

	subq := &ScalarSubqueryExpr{
		Query: &GeneralSelectStmt{
			RawSQL: "SELECT price, other_col, extra_col FROM items WHERE id = 1",
		},
	}

	for i := 0; i < 20; i++ {
		val, err := EvalRowExpr(subq, nil, mockExec)
		if err != nil {
			t.Fatalf("run %d failed: %v", i, err)
		}
		if val != 150 {
			t.Fatalf("run %d: expected 150 (from price column), got %v", i, val)
		}
	}
}
