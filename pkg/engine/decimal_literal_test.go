package engine

import (
	"fmt"
	"testing"

	"lionsql/pkg/storage"
)

func TestDecimalLiteralPrecision(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tests := []struct {
		sql      string
		wantStr  string
		wantType storage.DataType
	}{
		{
			sql:      "SELECT 1.1 + 2.2 AS res;",
			wantStr:  "3.3",
			wantType: storage.TypeDecimal,
		},
		{
			sql:      "SELECT 3.5 - 1.2 AS res;",
			wantStr:  "2.3",
			wantType: storage.TypeDecimal,
		},
		{
			sql:      "SELECT 1.5 * 2 AS res;",
			wantStr:  "3.0",
			wantType: storage.TypeDecimal,
		},
		{
			sql:      "SELECT CASE WHEN 1.1 + 2.2 = 3.3 THEN 'exact' ELSE 'loss' END AS res;",
			wantStr:  "exact",
			wantType: storage.TypeVarChar,
		},
	}

	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			res, err := coord.ExecuteSQL(tt.sql)
			if err != nil {
				t.Fatalf("Query failed: %v", err)
			}
			if len(res.Rows) != 1 {
				t.Fatalf("expected 1 row, got %d", len(res.Rows))
			}
			got := res.Rows[0]["res"]
			gotStr := fmt.Sprintf("%v", got)
			if gotVal, ok := got.(storage.Value); ok {
				gotStr = gotVal.String()
				if tt.wantType != 0 && gotVal.Type != tt.wantType {
					t.Errorf("expected type %v, got %v", tt.wantType, gotVal.Type)
				}
			}
			if gotStr != tt.wantStr {
				t.Errorf("expected %q, got %q", tt.wantStr, gotStr)
			}
		})
	}
}
