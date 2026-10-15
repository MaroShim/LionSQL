package engine_test

import (
	"fmt"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage/memengine"
)

func TestStringSplit_Basic(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Basic comma split
	res, err := coord.ExecuteSQL("SELECT value FROM STRING_SPLIT('apple,banana,cherry', ',');")
	if err != nil || !res.Success {
		t.Fatalf("STRING_SPLIT failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	expected := []string{"apple", "banana", "cherry"}
	for i, exp := range expected {
		val := fmt.Sprintf("%v", res.Rows[i]["value"])
		if val != exp {
			t.Errorf("row %d: expected %s, got %s", i, exp, val)
		}
	}

	// 2. Space separator
	res, err = coord.ExecuteSQL("SELECT value FROM STRING_SPLIT('one two three', ' ');")
	if err != nil || !res.Success {
		t.Fatalf("space separator failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}

	// 3. Ordinal enabled (3rd arg = 1)
	res, err = coord.ExecuteSQL("SELECT value, ordinal FROM STRING_SPLIT('gold,silver,bronze', ',', 1);")
	if err != nil || !res.Success {
		t.Fatalf("ordinal failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	for i, expVal := range []string{"gold", "silver", "bronze"} {
		v := fmt.Sprintf("%v", res.Rows[i]["value"])
		o := fmt.Sprintf("%v", res.Rows[i]["ordinal"])
		if v != expVal || o != fmt.Sprintf("%d", i+1) {
			t.Errorf("row %d: expected value=%s, ordinal=%d; got value=%s, ordinal=%s", i, expVal, i+1, v, o)
		}
	}

	// 4. Projection wildcard *
	res, err = coord.ExecuteSQL("SELECT * FROM STRING_SPLIT('alpha,beta', ',', 1);")
	if err != nil || !res.Success {
		t.Fatalf("wildcard with ordinal failed: %v", err)
	}
	if len(res.Columns) != 2 || res.Columns[0] != "value" || res.Columns[1] != "ordinal" {
		t.Errorf("expected columns [value, ordinal], got %v", res.Columns)
	}

	// 5. Empty string returns 0 rows
	res, err = coord.ExecuteSQL("SELECT value FROM STRING_SPLIT('', ',');")
	if err != nil || !res.Success {
		t.Fatalf("empty string failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows for empty string, got %d", len(res.Rows))
	}

	// 6. Escaped single quote
	res, err = coord.ExecuteSQL("SELECT value FROM STRING_SPLIT('O''Reilly,Smith', ',');")
	if err != nil || !res.Success {
		t.Fatalf("escaped quote failed: %v", err)
	}
	if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[0]["value"]) != "O'Reilly" {
		t.Errorf("expected O'Reilly, got %v", res.Rows[0]["value"])
	}
}

func TestStringSplit_ClausesAndPagination(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Table alias and WHERE filter
	res, err := coord.ExecuteSQL("SELECT s.value FROM STRING_SPLIT('a,b,c,d', ',') AS s WHERE s.value <> 'b';")
	if err != nil || !res.Success {
		t.Fatalf("alias and WHERE failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}

	// 2. ORDER BY
	res, err = coord.ExecuteSQL("SELECT value FROM STRING_SPLIT('charlie,alpha,bravo', ',') ORDER BY value ASC;")
	if err != nil || !res.Success {
		t.Fatalf("ORDER BY failed: %v", err)
	}
	if len(res.Rows) != 3 || fmt.Sprintf("%v", res.Rows[0]["value"]) != "alpha" {
		t.Errorf("expected first row alpha, got %v", res.Rows[0]["value"])
	}

	// 3. DISTINCT
	res, err = coord.ExecuteSQL("SELECT DISTINCT value FROM STRING_SPLIT('x,y,x,z,y', ',') ORDER BY value ASC;")
	if err != nil || !res.Success {
		t.Fatalf("DISTINCT failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 distinct rows, got %d", len(res.Rows))
	}

	// 4. COUNT(*)
	res, err = coord.ExecuteSQL("SELECT COUNT(*) AS total FROM STRING_SPLIT('10,20,30,40,50', ',');")
	if err != nil || !res.Success {
		t.Fatalf("COUNT(*) failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["total"]) != "5" {
		t.Errorf("expected count 5, got %v", res.Rows)
	}

	// 5. T-SQL OFFSET ... ROWS FETCH NEXT ... ROWS ONLY pagination
	res, err = coord.ExecuteSQL("SELECT value FROM STRING_SPLIT('a,b,c,d,e', ',') OFFSET 2 ROWS FETCH NEXT 2 ROWS ONLY;")
	if err != nil || !res.Success {
		t.Fatalf("OFFSET FETCH failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["value"]) != "c" || fmt.Sprintf("%v", res.Rows[1]["value"]) != "d" {
		t.Errorf("expected c, d; got %v, %v", res.Rows[0]["value"], res.Rows[1]["value"])
	}
}

func TestStringSplit_SessionVariablesAndSubqueries(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	sessionID := "test_sess_split"

	// 1. Session variable resolution
	_, err := coord.ExecuteSessionSQL(sessionID, "SET @tags = 'golang,database,engine';")
	if err != nil {
		t.Fatalf("SET @tags failed: %v", err)
	}
	res, err := coord.ExecuteSessionSQL(sessionID, "SELECT value FROM STRING_SPLIT(@tags, ',');")
	if err != nil || !res.Success {
		t.Fatalf("STRING_SPLIT with @tags failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows from @tags, got %d", len(res.Rows))
	}

	// 2. IN (SELECT value FROM STRING_SPLIT(...)) subquery
	_, err = coord.ExecuteSQL("CREATE TABLE items (id INT PRIMARY KEY, name VARCHAR(50));")
	if err != nil {
		t.Fatalf("CREATE TABLE items failed: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO items (id, name) VALUES (1, 'Book'), (2, 'Pen'), (3, 'Notebook'), (4, 'Eraser');")
	if err != nil {
		t.Fatalf("INSERT items failed: %v", err)
	}

	// Query with literal subquery
	res, err = coord.ExecuteSQL("SELECT id, name FROM items WHERE id IN (SELECT value FROM STRING_SPLIT('2,3', ',')) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("IN (SELECT STRING_SPLIT) failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["id"]) != "2" || fmt.Sprintf("%v", res.Rows[1]["id"]) != "3" {
		t.Errorf("expected ids 2 and 3, got %v and %v", res.Rows[0]["id"], res.Rows[1]["id"])
	}

	// Query with session variable subquery
	_, err = coord.ExecuteSessionSQL(sessionID, "SET @selected_ids = '1,4';")
	if err != nil {
		t.Fatalf("SET @selected_ids failed: %v", err)
	}
	res, err = coord.ExecuteSessionSQL(sessionID, "SELECT id, name FROM items WHERE id IN (SELECT value FROM STRING_SPLIT(@selected_ids, ',')) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("IN (SELECT STRING_SPLIT(@selected_ids)) failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["id"]) != "1" || fmt.Sprintf("%v", res.Rows[1]["id"]) != "4" {
		t.Errorf("expected ids 1 and 4, got %v and %v", res.Rows[0]["id"], res.Rows[1]["id"])
	}
}

func TestStringSplit_CrossApply(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE posts (id INT PRIMARY KEY, tags VARCHAR(100));")
	if err != nil {
		t.Fatalf("CREATE TABLE posts failed: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO posts (id, tags) VALUES (1, 'tech,news'), (2, 'sports,life,hobby');")
	if err != nil {
		t.Fatalf("INSERT posts failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT p.id, s.value FROM posts p CROSS APPLY STRING_SPLIT(p.tags, ',') s ORDER BY p.id ASC, s.value ASC;")
	if err != nil || !res.Success {
		t.Fatalf("CROSS APPLY STRING_SPLIT failed: %v", err)
	}
	if len(res.Rows) != 5 {
		t.Fatalf("expected 5 rows from CROSS APPLY, got %d", len(res.Rows))
	}
}
