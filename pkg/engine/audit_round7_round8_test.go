package engine

import (
	"fmt"
	"strings"
	"testing"
)

func TestEngine_AuditRound7Round8_Regression(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tests := []struct {
		name        string
		sql         string
		wantErr     bool
		errContains string
		wantVal     string
	}{
		// Round 7 #2 / Round 8: Arity checks
		{
			name:        "R7-050_LEN_too_few",
			sql:         "SELECT LEN() AS res;",
			wantErr:     true,
			errContains: "requires 1 argument",
		},
		{
			name:        "R7-050_LEN_too_many",
			sql:         "SELECT LEN('a', 'b') AS res;",
			wantErr:     true,
			errContains: "requires 1 argument",
		},
		{
			name:        "R7-029_GETDATE_too_many",
			sql:         "SELECT GETDATE(1) AS res;",
			wantErr:     true,
			errContains: "requires 0 argument",
		},
		{
			name:        "R7-062_COUNT_too_few",
			sql:         "SELECT COUNT() AS res;",
			wantErr:     true,
			errContains: "requires 1 argument",
		},
		{
			name:        "R7-063_SUM_too_few",
			sql:         "SELECT SUM() AS res;",
			wantErr:     true,
			errContains: "requires 1 argument",
		},
		{
			name:        "R7-063_SUM_too_many",
			sql:         "SELECT SUM(1, 2) AS res;",
			wantErr:     true,
			errContains: "requires 1 argument",
		},
		{
			name:        "R7-074_NULLIF_too_few",
			sql:         "SELECT NULLIF(1) AS res;",
			wantErr:     true,
			errContains: "requires 2 argument",
		},
		{
			name:        "R7-074_NULLIF_too_many",
			sql:         "SELECT NULLIF(1, 2, 3) AS res;",
			wantErr:     true,
			errContains: "requires 2 argument",
		},
		{
			name:        "R7-077_COALESCE_too_few",
			sql:         "SELECT COALESCE() AS res;",
			wantErr:     true,
			errContains: "requires at least 1 argument",
		},
		{
			name:        "R7-076_IIF_too_few",
			sql:         "SELECT IIF(1=1, 'a') AS res;",
			wantErr:     true,
			errContains: "requires 3 argument",
		},
		{
			name:        "R7-076_IIF_too_many",
			sql:         "SELECT IIF(1=1, 'a', 'b', 'c') AS res;",
			wantErr:     true,
			errContains: "requires 3 argument",
		},
		{
			name:        "R7-080_MD5_too_many",
			sql:         "SELECT MD5('a', 'b') AS res;",
			wantErr:     true,
			errContains: "requires 1 argument",
		},

		// Round 8 Area 3: Math and String functions
		{
			name:    "R8-037_ATAN2_Valid",
			sql:     "SELECT ATAN2(0.0, 1.0) AS res;",
			wantVal: "0",
		},
		{
			name:    "R8-038_BTRIM_Valid",
			sql:     "SELECT BTRIM('xyxhelloxyx', 'xy') AS res;",
			wantVal: "hello",
		},
		{
			name:    "Math_CBRT_Valid",
			sql:     "SELECT CBRT(27.0) AS res;",
			wantVal: "3",
		},
		{
			name:    "String_STR_Valid",
			sql:     "SELECT STR(123.45, 6, 1) AS res;",
			wantVal: " 123.5",
		},

		// Round 8 Area 3: Date unit validation and Msg 241
		{
			name:        "R7-030_DATEADD_invalid_unit",
			sql:         "SELECT DATEADD(unknown_unit, 1, '2026-01-01') AS res;",
			wantErr:     true,
			errContains: "is not a recognized dateadd option",
		},
		{
			name:        "R8-031_YEAR_invalid_date_msg241",
			sql:         "SELECT YEAR('invalid_date_str') AS res;",
			wantErr:     true,
			errContains: "Conversion failed when converting date and/or time from character string",
		},
		{
			name:        "R8-032_EOMONTH_invalid_date_msg241",
			sql:         "SELECT EOMONTH('not_a_date') AS res;",
			wantErr:     true,
			errContains: "Conversion failed when converting date and/or time from character string",
		},

		// Round 8 Area 3: HASHBYTES validation (Msg 8116)
		{
			name:        "R8-036_HASHBYTES_invalid_algo",
			sql:         "SELECT HASHBYTES('INVALID_ALGO', 'data') AS res;",
			wantErr:     true,
			errContains: "Msg 8116",
		},

		// Round 8 Area 3: System functions
		{
			name: "R8-040_SESSION_CONTEXT_Valid",
			sql:  "SELECT SESSION_CONTEXT(N'user_id') AS res;",
		},

		// IIF Short-circuiting verification
		{
			name:    "IIF_short_circuit_true",
			sql:     "SELECT IIF(1=1, 'success', 1/0) AS res;",
			wantVal: "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := coord.ExecuteSQL(tt.sql)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got success: %+v", tt.errContains, res)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error containing %q, got: %v", tt.errContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantVal != "" {
				if len(res.Rows) != 1 {
					t.Fatalf("expected 1 row, got %d", len(res.Rows))
				}
				got := fmt.Sprintf("%v", res.Rows[0]["res"])
				if strings.TrimSpace(got) != strings.TrimSpace(tt.wantVal) {
					t.Fatalf("expected %q, got %q", tt.wantVal, got)
				}
			}
		})
	}
}

func TestEngine_AuditRound8_WindowFrame_Regression(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	_, err := coord.ExecuteSQL("CREATE TABLE wf_t (id INT, v INT); INSERT INTO wf_t VALUES (1, 10), (2, 20);")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	tests := []struct {
		name        string
		sql         string
		wantErr     bool
		errContains string
	}{
		{
			name:        "R8-019_ROWS_unbounded_following_start",
			sql:         "SELECT SUM(v) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED FOLLOWING AND CURRENT ROW) FROM wf_t;",
			wantErr:     true,
			errContains: "UNBOUNDED FOLLOWING",
		},
		{
			name:        "R8-020_ROWS_following_before_preceding",
			sql:         "SELECT SUM(v) OVER (ORDER BY id ROWS BETWEEN 5 FOLLOWING AND 1 PRECEDING) FROM wf_t;",
			wantErr:     true,
			errContains: "cannot be FOLLOWING if frame end is PRECEDING",
		},
		{
			name:        "R8-021_ROWS_start_offset_greater_than_end",
			sql:         "SELECT SUM(v) OVER (ORDER BY id ROWS BETWEEN 5 PRECEDING AND 10 PRECEDING) FROM wf_t;",
			wantErr:     true,
			errContains: "cannot be smaller than frame end offset",
		},
		{
			name:        "R8-027_ROW_NUMBER_missing_ORDER_BY",
			sql:         "SELECT ROW_NUMBER() OVER () FROM wf_t;",
			wantErr:     true,
			errContains: "must have an OVER clause with ORDER BY",
		},
		{
			name:        "R8-028_RANK_missing_ORDER_BY",
			sql:         "SELECT RANK() OVER () FROM wf_t;",
			wantErr:     true,
			errContains: "must have an OVER clause with ORDER BY",
		},
		{
			name:        "R8-029_DENSE_RANK_missing_ORDER_BY",
			sql:         "SELECT DENSE_RANK() OVER () FROM wf_t;",
			wantErr:     true,
			errContains: "must have an OVER clause with ORDER BY",
		},
		{
			name:        "R8-030_NTILE_missing_ORDER_BY",
			sql:         "SELECT NTILE(2) OVER () FROM wf_t;",
			wantErr:     true,
			errContains: "must have an OVER clause with ORDER BY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := coord.ExecuteSQL(tt.sql)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got success: %+v", tt.errContains, res)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error containing %q, got: %v", tt.errContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
