package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestDEFECT_AllClauses_FailClosed(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_clauses_failclosed"
	coord.GetOrCreateSession(sessID)

	_, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE t (id INT PRIMARY KEY, val INT);")
	if err != nil {
		t.Fatalf("setup table failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sessID, "INSERT INTO t VALUES (1, 10), (2, 20);")
	if err != nil {
		t.Fatalf("insert data failed: %v", err)
	}

	testCases := []struct {
		name string
		sql  string
	}{
		// 1. ORDER BY defects
		{"empty ORDER BY", "SELECT * FROM t ORDER BY;"},
		{"malformed ORDER BY token", "SELECT * FROM t ORDER BY id %%%;"},
		{"trailing comma ORDER BY", "SELECT * FROM t ORDER BY id,;"},
		{"empty ORDER BY on scalar SELECT", "SELECT 1 ORDER BY;"},

		// 2. GROUP BY defects
		{"empty GROUP BY", "SELECT val FROM t GROUP BY;"},
		{"malformed GROUP BY expr", "SELECT val FROM t GROUP BY val %%%;"},
		{"trailing comma GROUP BY", "SELECT val FROM t GROUP BY val,;"},

		// 3. HAVING defects
		{"empty HAVING", "SELECT val, COUNT(*) FROM t GROUP BY val HAVING;"},
		{"malformed HAVING", "SELECT val, COUNT(*) FROM t GROUP BY val HAVING COUNT(*) = %%%;"},

		// 4. LIMIT defects
		{"empty LIMIT", "SELECT * FROM t LIMIT;"},
		{"non-integer LIMIT", "SELECT * FROM t LIMIT abc;"},
		{"negative LIMIT", "SELECT * FROM t LIMIT -5;"},

		// 5. OFFSET defects
		{"empty OFFSET", "SELECT * FROM t OFFSET;"},
		{"non-integer OFFSET", "SELECT * FROM t OFFSET abc;"},
		{"negative OFFSET", "SELECT * FROM t OFFSET -5;"},

		// 6. UPDATE SET defects
		{"empty UPDATE SET", "UPDATE t SET;"},
		{"empty RHS in UPDATE SET", "UPDATE t SET val = ;"},
		{"malformed RHS in UPDATE SET", "UPDATE t SET val = %%%;"},
		{"trailing comma in UPDATE SET", "UPDATE t SET val = 1,;"},

		// 7. INSERT VALUES defects
		{"empty INSERT VALUES", "INSERT INTO t VALUES;"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := coord.ExecuteSessionSQL(sessID, tc.sql)
			if err == nil {
				t.Errorf("DEFECT CONFIRMED: query %q succeeded with result %+v, expected syntax error!", tc.sql, res)
			} else {
				t.Logf("PASS: query %q correctly failed with error: %v", tc.sql, err)
			}
		})
	}
}
