package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestDEFECT_Auxiliary_MalformedWhere(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_aux_where"
	coord.GetOrCreateSession(sessID)

	// 1. STRING_SPLIT with malformed WHERE
	res1, err1 := coord.ExecuteSessionSQL(sessID, "SELECT value FROM STRING_SPLIT('1,2,3', ',') WHERE id = %%%;")
	t.Logf("STRING_SPLIT malformed WHERE: res=%+v, err=%v", res1, err1)
	if err1 == nil {
		t.Errorf("DEFECT CONFIRMED: STRING_SPLIT with malformed WHERE succeeded!")
	}

	res1b, err1b := coord.ExecuteSessionSQL(sessID, "SELECT value FROM STRING_SPLIT('1,2,3', ',') WHERE;")
	t.Logf("STRING_SPLIT empty WHERE: res=%+v, err=%v", res1b, err1b)
	if err1b == nil {
		t.Errorf("DEFECT CONFIRMED: STRING_SPLIT with empty WHERE succeeded!")
	}

	// 2. OPENJSON with malformed WHERE
	res2, err2 := coord.ExecuteSessionSQL(sessID, `SELECT value FROM OPENJSON('["a", "b"]') WHERE id = %%%;`)
	t.Logf("OPENJSON malformed WHERE: res=%+v, err=%v", res2, err2)
	if err2 == nil {
		t.Errorf("DEFECT CONFIRMED: OPENJSON with malformed WHERE succeeded!")
	}

	res2b, err2b := coord.ExecuteSessionSQL(sessID, `SELECT value FROM OPENJSON('["a", "b"]') WHERE;`)
	t.Logf("OPENJSON empty WHERE: res=%+v, err=%v", res2b, err2b)
	if err2b == nil {
		t.Errorf("DEFECT CONFIRMED: OPENJSON with empty WHERE succeeded!")
	}

	// 3. ON CONFLICT DO UPDATE with malformed WHERE
	coord.ExecuteSessionSQL(sessID, "CREATE TABLE t_upsert (id INT PRIMARY KEY, val INT);")
	res3, err3 := coord.ExecuteSessionSQL(sessID, "INSERT INTO t_upsert VALUES (1, 10) ON CONFLICT (id) DO UPDATE SET val = 20 WHERE id = %%%;")
	t.Logf("ON CONFLICT DO UPDATE malformed WHERE: res=%+v, err=%v", res3, err3)
	if err3 == nil {
		t.Errorf("DEFECT CONFIRMED: ON CONFLICT DO UPDATE with malformed WHERE succeeded!")
	}

	res3b, err3b := coord.ExecuteSessionSQL(sessID, "INSERT INTO t_upsert VALUES (1, 10) ON CONFLICT (id) DO UPDATE SET val = 20 WHERE;")
	t.Logf("ON CONFLICT DO UPDATE empty WHERE: res=%+v, err=%v", res3b, err3b)
	if err3b == nil {
		t.Errorf("DEFECT CONFIRMED: ON CONFLICT DO UPDATE with empty WHERE succeeded!")
	}
}

func TestDEFECT_Pivot_MalformedWhere(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_pvt_where"
	coord.GetOrCreateSession(sessID)

	coord.ExecuteSessionSQL(sessID, "CREATE TABLE sales (year INT, quarter VARCHAR(10), amount INT);")
	coord.ExecuteSessionSQL(sessID, "INSERT INTO sales VALUES (2020, 'Q1', 100), (2020, 'Q2', 200);")

	pvtSQL := `
SELECT * FROM (
    SELECT year, quarter, amount FROM sales
) AS src
PIVOT (
    SUM(amount) FOR quarter IN ([Q1], [Q2])
) AS pvt
WHERE id = %%%;
`
	res, err := coord.ExecuteSessionSQL(sessID, pvtSQL)
	t.Logf("PIVOT malformed WHERE: res=%+v, err=%v", res, err)
	if err == nil {
		t.Errorf("DEFECT CONFIRMED: PIVOT with malformed WHERE succeeded!")
	}

	pvtEmptySQL := `
SELECT * FROM (
    SELECT year, quarter, amount FROM sales
) AS src
PIVOT (
    SUM(amount) FOR quarter IN ([Q1], [Q2])
) AS pvt
WHERE;
`
	resEmpty, errEmpty := coord.ExecuteSessionSQL(sessID, pvtEmptySQL)
	t.Logf("PIVOT empty WHERE: res=%+v, err=%v", resEmpty, errEmpty)
	if errEmpty == nil {
		t.Errorf("DEFECT CONFIRMED: PIVOT with empty WHERE succeeded!")
	}
}

func TestDEFECT_Merge_MalformedOnOrAnd(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessID := "test_merge_malformed_on"
	coord.GetOrCreateSession(sessID)

	coord.ExecuteSessionSQL(sessID, "CREATE TABLE m_t (id INT PRIMARY KEY, val INT);")
	coord.ExecuteSessionSQL(sessID, "CREATE TABLE m_s (id INT PRIMARY KEY, val INT);")

	// 1. MERGE with empty ON
	sqlEmptyOn := `
MERGE m_t AS t
USING m_s AS s
ON
WHEN NOT MATCHED THEN
    INSERT (id, val) VALUES (s.id, s.val);
`
	_, errEmptyOn := coord.ExecuteSessionSQL(sessID, sqlEmptyOn)
	if errEmptyOn == nil {
		t.Errorf("DEFECT CONFIRMED: MERGE with empty ON succeeded!")
	}

	// 2. MERGE with malformed ON
	sqlMalformedOn := `
MERGE m_t AS t
USING m_s AS s
ON t.id = %%%
WHEN NOT MATCHED THEN
    INSERT (id, val) VALUES (s.id, s.val);
`
	_, errMalformedOn := coord.ExecuteSessionSQL(sessID, sqlMalformedOn)
	if errMalformedOn == nil {
		t.Errorf("DEFECT CONFIRMED: MERGE with malformed ON succeeded!")
	}

	// 3. MERGE with malformed WHEN AND
	sqlMalformedWhenAnd := `
MERGE m_t AS t
USING m_s AS s
ON t.id = s.id
WHEN MATCHED AND t.val = %%% THEN
    UPDATE SET val = s.val;
`
	_, errMalformedWhenAnd := coord.ExecuteSessionSQL(sessID, sqlMalformedWhenAnd)
	if errMalformedWhenAnd == nil {
		t.Errorf("DEFECT CONFIRMED: MERGE with malformed WHEN AND condition succeeded!")
	}
}
