package planner_test

import (
	"reflect"
	"testing"

	"sqlion/pkg/planner"
	"sqlion/pkg/storage"
)

func TestPrunePartitions_Monthly(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "events",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "created_at", Type: storage.TypeDateTime},
		},
		Partition: &storage.PartitionDef{
			Expr:   "toYYYYMM(created_at)",
			Column: "created_at",
			Func:   "TOYYYYMM",
		},
	}

	available := []string{"202607", "202608", "202609", "202610", "202611", "202612"}

	tests := []struct {
		name     string
		conds    []string
		expected []string
	}{
		{
			name:     "No conditions on partition column",
			conds:    []string{"id = 1"},
			expected: nil,
		},
		{
			name:     "Exact month equality",
			conds:    []string{"created_at = '2026-10-15 12:00:00'"},
			expected: []string{"202610"},
		},
		{
			name:     "IN clause with two months",
			conds:    []string{"created_at IN ('2026-08-01', '2026-10-01')"},
			expected: []string{"202608", "202610"},
		},
		{
			name:     "Range query covering September and October",
			conds:    []string{"created_at >= '2026-09-01'", "created_at < '2026-11-01'"},
			expected: []string{"202609", "202610"},
		},
		{
			name:     "BETWEEN query covering August to October",
			conds:    []string{"created_at BETWEEN '2026-08-01' AND '2026-10-31'"},
			expected: []string{"202608", "202609", "202610"},
		},
		{
			name:     "Lower bound only",
			conds:    []string{"created_at >= '2026-11-01'"},
			expected: []string{"202611", "202612"},
		},
		{
			name:     "Upper bound only",
			conds:    []string{"created_at <= '2026-08-31'"},
			expected: []string{"202607", "202608"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := planner.PrunePartitions(schema, tc.conds, available)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("PrunePartitions(%v) = %v, expected %v", tc.conds, got, tc.expected)
			}
		})
	}
}

func TestPrunePartitions_Daily(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "clicks",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "clicked_at", Type: storage.TypeDate},
		},
		Partition: &storage.PartitionDef{
			Expr:   "DAY(clicked_at)",
			Column: "clicked_at",
			Func:   "DAY",
		},
	}

	available := []string{"20261001", "20261002", "20261003", "20261004", "20261005"}

	got := planner.PrunePartitions(schema, []string{"clicked_at = '2026-10-03'"}, available)
	expected := []string{"20261003"}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("expected %v, got %v", expected, got)
	}

	rangeGot := planner.PrunePartitions(schema, []string{"clicked_at >= '2026-10-02'", "clicked_at <= '2026-10-04'"}, available)
	rangeExpected := []string{"20261002", "20261003", "20261004"}
	if !reflect.DeepEqual(rangeGot, rangeExpected) {
		t.Errorf("expected %v, got %v", rangeExpected, rangeGot)
	}
}
