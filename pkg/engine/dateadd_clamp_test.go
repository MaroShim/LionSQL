package engine

import (
	"fmt"
	"testing"
)

func TestEngine_DateAdd_MonthEndClamping(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "DateAdd_Month_Jan31_To_Feb28",
			query:    "SELECT DATEADD(month, 1, '2026-01-31') AS dt;",
			expected: "2026-02-28",
		},
		{
			name:     "DateAdd_Month_Jan31_LeapYear_To_Feb29",
			query:    "SELECT DATEADD(month, 1, '2024-01-31') AS dt;",
			expected: "2024-02-29",
		},
		{
			name:     "DateAdd_Year_Feb29_To_Feb28",
			query:    "SELECT DATEADD(year, 1, '2024-02-29') AS dt;",
			expected: "2025-02-28",
		},
		{
			name:     "DateAdd_Quarter_Jan31_To_Apr30",
			query:    "SELECT DATEADD(quarter, 1, '2026-01-31') AS dt;",
			expected: "2026-04-30",
		},
		{
			name:     "DateAdd_NegativeMonth_Mar31_To_Feb28",
			query:    "SELECT DATEADD(month, -1, '2026-03-31') AS dt;",
			expected: "2026-02-28",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := coord.ExecuteSQL(tc.query)
			if err != nil {
				t.Fatalf("failed to execute query %q: %v", tc.query, err)
			}
			if len(res.Rows) != 1 {
				t.Fatalf("expected 1 row, got %d", len(res.Rows))
			}
			got := fmt.Sprintf("%v", res.Rows[0]["dt"])
			if len(got) < len(tc.expected) || got[:len(tc.expected)] != tc.expected {
				t.Errorf("expected date prefix %q, got %q", tc.expected, got)
			}
		})
	}
}
