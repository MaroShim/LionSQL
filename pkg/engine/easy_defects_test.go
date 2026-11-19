package engine

import (
	"strings"
	"testing"
)

// TestEngine_NegativeLengthStringFunctions verifies [R2 N-12]:
// LEFT, RIGHT, and SUBSTRING with negative length arguments must raise error (Msg 536).
func TestEngine_NegativeLengthStringFunctions(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tests := []struct {
		name      string
		sql       string
		expectErr string
	}{
		{
			name:      "Left_NegativeLength",
			sql:       "SELECT LEFT('hello', -1) AS res;",
			expectErr: "Invalid length parameter passed to the LEFT or SUBSTRING function",
		},
		{
			name:      "Right_NegativeLength",
			sql:       "SELECT RIGHT('hello', -2) AS res;",
			expectErr: "Invalid length parameter passed to the LEFT or SUBSTRING function",
		},
		{
			name:      "Substring_NegativeLength",
			sql:       "SELECT SUBSTRING('hello', 1, -1) AS res;",
			expectErr: "Invalid length parameter passed to the LEFT or SUBSTRING function",
		},
		{
			name:      "Left_ZeroLength_Ok",
			sql:       "SELECT LEFT('hello', 0) AS res;",
			expectErr: "",
		},
		{
			name:      "Right_ZeroLength_Ok",
			sql:       "SELECT RIGHT('hello', 0) AS res;",
			expectErr: "",
		},
		{
			name:      "Substring_ZeroLength_Ok",
			sql:       "SELECT SUBSTRING('hello', 1, 0) AS res;",
			expectErr: "",
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
				if got != "" {
					t.Fatalf("expected empty string, got %v", got)
				}
			}
		})
	}
}

// TestEngine_MathDomainErrors verifies [R2 N-10]:
// Mathematical functions outside their valid domains must raise an error (Msg 3623).
func TestEngine_MathDomainErrors(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tests := []struct {
		name      string
		sql       string
		expectErr string
	}{
		{
			name:      "Sqrt_Negative",
			sql:       "SELECT SQRT(-1) AS res;",
			expectErr: "An invalid floating point operation occurred",
		},
		{
			name:      "Log_Zero",
			sql:       "SELECT LOG(0) AS res;",
			expectErr: "An invalid floating point operation occurred",
		},
		{
			name:      "Log_Negative",
			sql:       "SELECT LOG(-5) AS res;",
			expectErr: "An invalid floating point operation occurred",
		},
		{
			name:      "Log_BaseZero",
			sql:       "SELECT LOG(10, 0) AS res;",
			expectErr: "An invalid floating point operation occurred",
		},
		{
			name:      "Log_BaseOne",
			sql:       "SELECT LOG(10, 1) AS res;",
			expectErr: "An invalid floating point operation occurred",
		},
		{
			name:      "Log10_Zero",
			sql:       "SELECT LOG10(0) AS res;",
			expectErr: "An invalid floating point operation occurred",
		},
		{
			name:      "Log10_Negative",
			sql:       "SELECT LOG10(-10) AS res;",
			expectErr: "An invalid floating point operation occurred",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := coord.ExecuteSQL(tt.sql)
			if err == nil {
				t.Fatalf("expected error containing %q, got success: %+v", tt.expectErr, res)
			}
			if !strings.Contains(err.Error(), tt.expectErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.expectErr, err)
			}
		})
	}
}

// TestEngine_DuplicateVariableDeclaration verifies [R1 결함 10]:
// Redeclaring a procedure parameter or duplicate DECLARE in the same scope must fail compilation.
func TestEngine_DuplicateVariableDeclaration(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tests := []struct {
		name      string
		sql       string
		expectErr string
	}{
		{
			name: "ParamShadowingByDeclare",
			sql: `CREATE PROCEDURE sp_test_shadow_param @val INT AS
BEGIN
    DECLARE @val INT;
    SELECT @val;
END;`,
			expectErr: "The variable name '@val' has already been declared",
		},
		{
			name: "DuplicateDeclareInSameScope",
			sql: `CREATE PROCEDURE sp_test_dup_declare AS
BEGIN
    DECLARE @x INT;
    DECLARE @x INT;
    SELECT @x;
END;`,
			expectErr: "The variable name '@x' has already been declared",
		},
		{
			name: "CaseInsensitiveDuplicateDeclare",
			sql: `CREATE PROCEDURE sp_test_ci_dup_declare @TotalAmount MONEY AS
BEGIN
    DECLARE @totalamount MONEY;
    SELECT @totalamount;
END;`,
			expectErr: "The variable name '@totalamount' has already been declared",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := coord.ExecuteSQL(tt.sql)
			if err == nil {
				t.Fatalf("expected error containing %q, got success", tt.expectErr)
			}
			if !strings.Contains(err.Error(), tt.expectErr) {
				t.Fatalf("expected error containing %q, got: %v", tt.expectErr, err)
			}
		})
	}
}

// TestEngine_FromPartsAllSixValidation verifies all 6 *FROMPARTS constructors:
// DATEFROMPARTS, TIMEFROMPARTS, DATETIMEFROMPARTS, SMALLDATETIMEFROMPARTS,
// DATETIME2FROMPARTS, and DATETIMEOFFSETFROMPARTS (R3-22, R4-10, R5-083, R5-084).
func TestEngine_FromPartsAllSixValidation(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tests := []struct {
		name      string
		sql       string
		expectVal string
		expectErr string
	}{
		{
			name:      "DateFromParts_Valid",
			sql:       "SELECT DATEFROMPARTS(2026, 2, 28) AS res;",
			expectVal: "2026-02-28",
		},
		{
			name:      "DateFromParts_InvalidFeb30_R3_22",
			sql:       "SELECT DATEFROMPARTS(2026, 2, 30) AS res;",
			expectErr: "Cannot construct data type date",
		},
		{
			name:      "TimeFromParts_Valid",
			sql:       "SELECT TIMEFROMPARTS(14, 30, 15, 5, 2) AS res;",
			expectVal: "14:30:15.05",
		},
		{
			name:      "TimeFromParts_InvalidHour25_R5_083",
			sql:       "SELECT TIMEFROMPARTS(25, 0, 0, 0, 0) AS res;",
			expectErr: "Cannot construct data type time",
		},
		{
			name:      "DateTimeFromParts_Valid",
			sql:       "SELECT DATETIMEFROMPARTS(2026, 11, 19, 14, 30, 0, 123) AS res;",
			expectVal: "2026-11-19 14:30:00.123",
		},
		{
			name:      "DateTimeFromParts_InvalidHour25_R4_10",
			sql:       "SELECT DATETIMEFROMPARTS(2026, 11, 19, 25, 0, 0, 0) AS res;",
			expectErr: "Cannot construct data type datetime",
		},
		{
			name:      "SmallDateTimeFromParts_Valid",
			sql:       "SELECT SMALLDATETIMEFROMPARTS(2026, 11, 19, 14, 30) AS res;",
			expectVal: "2026-11-19 14:30:00",
		},
		{
			name:      "SmallDateTimeFromParts_InvalidFeb30_R5_084",
			sql:       "SELECT SMALLDATETIMEFROMPARTS(2026, 2, 30, 0, 0) AS res;",
			expectErr: "Cannot construct data type smalldatetime",
		},
		{
			name:      "DateTime2FromParts_Valid",
			sql:       "SELECT DATETIME2FROMPARTS(2026, 11, 19, 14, 30, 45, 50, 3) AS res;",
			expectVal: "2026-11-19 14:30:45.050",
		},
		{
			name:      "DateTime2FromParts_InvalidFeb30",
			sql:       "SELECT DATETIME2FROMPARTS(2026, 2, 30, 14, 30, 45, 0, 0) AS res;",
			expectErr: "Cannot construct data type datetime2",
		},
		{
			name:      "DateTime2FromParts_InvalidFractionForPrecision",
			sql:       "SELECT DATETIME2FROMPARTS(2026, 11, 19, 14, 30, 45, 500, 2) AS res;",
			expectErr: "Cannot construct data type datetime2",
		},
		{
			name:      "DateTimeOffsetFromParts_Valid",
			sql:       "SELECT DATETIMEOFFSETFROMPARTS(2026, 11, 19, 14, 30, 45, 5, 9, 0, 2) AS res;",
			expectVal: "2026-11-19 14:30:45.05 +09:00",
		},
		{
			name:      "DateTimeOffsetFromParts_InvalidOffset",
			sql:       "SELECT DATETIMEOFFSETFROMPARTS(2026, 11, 19, 14, 30, 45, 0, 15, 0, 0) AS res;",
			expectErr: "Cannot construct data type datetimeoffset",
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
				if got != tt.expectVal {
					t.Fatalf("expected %q, got %q", tt.expectVal, got)
				}
			}
		})
	}
}
