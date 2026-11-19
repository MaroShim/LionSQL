package engine

import (
	"fmt"
	"strings"
	"testing"
)

// TestEngine_CastIntegerOverflow verifies [R2 N-9]:
// CASTing a number exceeding the target integer type's bounds must raise an arithmetic overflow error,
// and TRY_CAST must return NULL without error.
func TestEngine_CastIntegerOverflow(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tests := []struct {
		name      string
		sql       string
		expectErr string
		expectVal string
	}{
		{
			name:      "Cast_Int_Overflow_3Billion",
			sql:       "SELECT CAST(3000000000 AS INT) AS res;",
			expectErr: "arithmetic overflow error converting 3000000000 to data type int",
		},
		{
			name:      "TryCast_Int_Overflow_3Billion",
			sql:       "SELECT TRY_CAST(3000000000 AS INT) AS res;",
			expectVal: "<nil>",
		},
		{
			name:      "Cast_Int_Underflow",
			sql:       "SELECT CAST(-3000000000 AS INT) AS res;",
			expectErr: "arithmetic overflow error converting -3000000000 to data type int",
		},
		{
			name:      "Cast_Smallint_Overflow",
			sql:       "SELECT CAST(40000 AS SMALLINT) AS res;",
			expectErr: "arithmetic overflow error converting 40000 to data type smallint",
		},
		{
			name:      "Cast_Tinyint_Overflow",
			sql:       "SELECT CAST(300 AS TINYINT) AS res;",
			expectErr: "arithmetic overflow error converting 300 to data type tinyint",
		},
		{
			name:      "Cast_Tinyint_Negative",
			sql:       "SELECT CAST(-1 AS TINYINT) AS res;",
			expectErr: "arithmetic overflow error converting -1 to data type tinyint",
		},
		{
			name:      "Cast_Bigint_Ok",
			sql:       "SELECT CAST(3000000000 AS BIGINT) AS res;",
			expectVal: "3000000000",
		},
		{
			name:      "Cast_Int_Max_Ok",
			sql:       "SELECT CAST(2147483647 AS INT) AS res;",
			expectVal: "2147483647",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := coord.ExecuteSQL(tt.sql)
			if tt.expectErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got success: %+v", tt.expectErr, res)
				}
				if !strings.Contains(err.Error(), tt.expectErr) {
					t.Fatalf("expected error containing %q, got: %v", tt.expectErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}
				if len(res.Rows) != 1 {
					t.Fatalf("expected 1 row, got %d", len(res.Rows))
				}
				got := res.Rows[0]["res"]
				gotStr := fmt.Sprintf("%v", got)
				if gotStr != tt.expectVal {
					t.Fatalf("expected %q, got %q", tt.expectVal, gotStr)
				}
			}
		})
	}
}
