package core

import "testing"

func TestStatusOrder(t *testing.T) {
	cases := []struct {
		status string
		want   int
	}{
		{"IN_PROGRESS", 0},
		{"in_progress", 0},
		{"PENDING", 1},
		{"DONE", 2},
		{"SPEC_OUT", 3},
	}
	for _, tc := range cases {
		if got := statusOrder(tc.status); got != tc.want {
			t.Fatalf("statusOrder(%q) = %d, want %d", tc.status, got, tc.want)
		}
	}
}
