package engine

import (
	"fmt"
	"strings"
	"testing"
)

// TestEngine_IntegerArithmeticOverflow verifies [R2 N-8]:
// 32-bit INT arithmetic exceeding INT range (2147483647 + 1) must raise an arithmetic overflow error (Msg 8115),
// while BIGINT arithmetic or mixed INT+BIGINT must safely compute within 64-bit bounds.
func TestEngine_IntegerArithmeticOverflow(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tests := []struct {
		name      string
		sql       string
		expectErr string
		expectVal string
	}{
		{
			name:      "Int_Addition_Overflow",
			sql:       "SELECT 2147483647 + 1 AS res;",
			expectErr: "Arithmetic overflow error converting expression to data type int",
		},
		{
			name:      "Int_Subtraction_Underflow",
			sql:       "SELECT -2147483648 - 1 AS res;",
			expectErr: "Arithmetic overflow error converting expression to data type int",
		},
		{
			name:      "Int_Multiplication_Overflow",
			sql:       "SELECT 2147483647 * 2 AS res;",
			expectErr: "Arithmetic overflow error converting expression to data type int",
		},
		{
			name:      "Bigint_Literal_Addition_Ok",
			sql:       "SELECT 2147483648 + 1 AS res;",
			expectVal: "2147483649",
		},
		{
			name:      "Bigint_Cast_Addition_Ok",
			sql:       "SELECT CAST(2147483647 AS BIGINT) + 1 AS res;",
			expectVal: "2147483648",
		},
		{
			name:      "Int_Normal_Addition_Ok",
			sql:       "SELECT 100 + 200 AS res;",
			expectVal: "300",
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

	t.Run("Int_Column_Addition_Overflow_Table", func(t *testing.T) {
		if _, err := coord.ExecuteSQL("CREATE TABLE r2_int_ovf (n INT PRIMARY KEY);"); err != nil {
			t.Fatalf("CREATE TABLE failed: %v", err)
		}
		if _, err := coord.ExecuteSQL("INSERT INTO r2_int_ovf VALUES (2147483647);"); err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}
		res, err := coord.ExecuteSQL("SELECT n + 1 AS res FROM r2_int_ovf;")
		if err == nil {
			t.Fatalf("expected arithmetic overflow error, got success: %+v", res.Rows)
		}
		if !strings.Contains(err.Error(), "Arithmetic overflow error converting expression to data type int") {
			t.Fatalf("expected Msg 8115 arithmetic overflow, got: %v", err)
		}
	})
}
