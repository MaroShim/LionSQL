package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestDEFECT_MalformedWhere_MustFail(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_malformed_where"
	coord.GetOrCreateSession(sessID)

	mustExec := func(sql string) {
		t.Helper()
		_, err := coord.ExecuteSessionSQL(sessID, sql)
		if err != nil {
			t.Fatalf("setup failed %q: %v", sql, err)
		}
	}

	mustExec("CREATE TABLE test_tab (id INT PRIMARY KEY, val INT);")
	mustExec("INSERT INTO test_tab VALUES (1, 100), (2, 200), (3, 300);")

	queries := []struct {
		name string
		sql  string
	}{
		{"SELECT empty WHERE", "SELECT * FROM test_tab WHERE;"},
		{"SELECT malformed WHERE", "SELECT * FROM test_tab WHERE id = %%%;"},
		{"SELECT dangling AND", "SELECT * FROM test_tab WHERE id = 1 AND;"},
		{"SELECT dangling OR", "SELECT * FROM test_tab WHERE id = 1 OR;"},
		{"SELECT standalone WHERE", "SELECT 1 WHERE;"},
		{"SELECT standalone malformed WHERE", "SELECT 1 WHERE 1 = %%%;"},
		{"UPDATE empty WHERE", "UPDATE test_tab SET val = 999 WHERE;"},
		{"UPDATE malformed WHERE", "UPDATE test_tab SET val = 999 WHERE id = %%%;"},
		{"UPDATE dangling AND", "UPDATE test_tab SET val = 999 WHERE id = 1 AND;"},
		{"DELETE empty WHERE", "DELETE FROM test_tab WHERE;"},
		{"DELETE malformed WHERE", "DELETE FROM test_tab WHERE id = %%%;"},
		{"DELETE dangling AND", "DELETE FROM test_tab WHERE id = 1 AND;"},
	}

	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			res, err := coord.ExecuteSessionSQL(sessID, tc.sql)
			if err == nil {
				t.Fatalf("DEFECT CONFIRMED: query %q succeeded instead of failing with syntax error! Result: %+v", tc.sql, res)
			}
		})
	}
}

func TestDEFECT_InsertSelect_MalformedWhere(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_insert_where"
	coord.GetOrCreateSession(sessID)

	coord.ExecuteSessionSQL(sessID, "CREATE TABLE src (id INT PRIMARY KEY);")
	coord.ExecuteSessionSQL(sessID, "INSERT INTO src VALUES (1), (2);")
	coord.ExecuteSessionSQL(sessID, "CREATE TABLE dst (id INT PRIMARY KEY);")

	// INSERT INTO dst SELECT id FROM src WHERE;
	res, err := coord.ExecuteSessionSQL(sessID, "INSERT INTO dst SELECT id FROM src WHERE;")
	if err == nil {
		t.Fatalf("DEFECT CONFIRMED: INSERT INTO ... SELECT ... WHERE; succeeded instead of syntax error! Result: %+v", res)
	}
}
