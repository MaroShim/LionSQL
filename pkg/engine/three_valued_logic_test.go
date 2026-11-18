package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestThreeValuedLogic_SQL(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// Probes for AND:
	// TRUE AND NULL => NULL
	// FALSE AND NULL => FALSE
	// NULL AND TRUE => NULL
	// NULL AND FALSE => FALSE
	// NULL AND NULL => NULL

	// Probes for OR:
	// TRUE OR NULL => TRUE
	// FALSE OR NULL => NULL
	// NULL OR TRUE => TRUE
	// NULL OR FALSE => NULL
	// NULL OR NULL => NULL

	tests := []struct {
		sql      string
		expected any // nil for NULL, true for TRUE, false for FALSE
	}{
		{"SELECT (true AND NULL) AS val;", nil},
		{"SELECT (false AND NULL) AS val;", false},
		{"SELECT (NULL AND true) AS val;", nil},
		{"SELECT (NULL AND false) AS val;", false},
		{"SELECT (NULL AND NULL) AS val;", nil},
		{"SELECT (true OR NULL) AS val;", true},
		{"SELECT (false OR NULL) AS val;", nil},
		{"SELECT (NULL OR true) AS val;", true},
		{"SELECT (NULL OR false) AS val;", nil},
		{"SELECT (NULL OR NULL) AS val;", nil},
	}

	for _, tt := range tests {
		res, err := coord.ExecuteSQL(tt.sql)
		if err != nil {
			t.Fatalf("query %q failed: %v", tt.sql, err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("query %q expected 1 row, got %v", tt.sql, res.Rows)
		}
		actual := res.Rows[0]["val"]
		if tt.expected == nil {
			if actual != nil {
				t.Errorf("query %q expected NULL, got %v (%T)", tt.sql, actual, actual)
			}
		} else {
			if actual != tt.expected {
				t.Errorf("query %q expected %v, got %v (%T)", tt.sql, tt.expected, actual, actual)
			}
		}
	}
}
