package engine_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestLegacyComparisonOperators(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE test_nums (id INT PRIMARY KEY, val INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	for i := 1; i <= 5; i++ {
		_, err = coord.ExecuteSQL(fmt.Sprintf("INSERT INTO test_nums VALUES (%d, %d);", i, i*10))
		if err != nil {
			t.Fatalf("INSERT failed: %v", err)
		}
	}

	// 1. !< (NOT LESS THAN == >= 30) -> ids 3, 4, 5
	res, err := coord.ExecuteSQL("SELECT id FROM test_nums WHERE val !< 30 ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("!< failed: %v", err)
	}
	if len(res.Rows) != 3 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "3" {
		t.Fatalf("!< expected 3 rows (3,4,5), got: %v", res.Rows)
	}

	// 2. !> (NOT GREATER THAN == <= 30) -> ids 1, 2, 3
	res, err = coord.ExecuteSQL("SELECT id FROM test_nums WHERE val !> 30 ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("!> failed: %v", err)
	}
	if len(res.Rows) != 3 || fmt.Sprintf("%v", res.Rows[2]["id"]) != "3" {
		t.Fatalf("!> expected 3 rows (1,2,3), got: %v", res.Rows)
	}

	// 3. Whitespace tolerance: ! < (BABEL-4114)
	res, err = coord.ExecuteSQL("SELECT id FROM test_nums WHERE val !  < 40 ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("!  < failed: %v", err)
	}
	if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "4" {
		t.Fatalf("!  < expected 2 rows (4,5), got: %v", res.Rows)
	}

	// 4. Whitespace tolerance: ! > (BABEL-4114)
	res, err = coord.ExecuteSQL("SELECT id FROM test_nums WHERE val !   > 20 ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("!   > failed: %v", err)
	}
	if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[1]["id"]) != "2" {
		t.Fatalf("!   > expected 2 rows (1,2), got: %v", res.Rows)
	}

	// 5. Whitespace tolerance: ! = (BABEL-3379)
	res, err = coord.ExecuteSQL("SELECT id FROM test_nums WHERE val ! = 30 ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("! = failed: %v", err)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("! = expected 4 rows, got: %v", res.Rows)
	}

	// 6. Whitespace tolerance: < = and > = and < >
	res, err = coord.ExecuteSQL("SELECT id FROM test_nums WHERE val <  = 20 ORDER BY id ASC;")
	if err != nil || len(res.Rows) != 2 {
		t.Fatalf("<  = failed: %v (rows: %v)", err, res.Rows)
	}
	res, err = coord.ExecuteSQL("SELECT id FROM test_nums WHERE val >   = 40 ORDER BY id ASC;")
	if err != nil || len(res.Rows) != 2 {
		t.Fatalf(">   = failed: %v (rows: %v)", err, res.Rows)
	}
	res, err = coord.ExecuteSQL("SELECT id FROM test_nums WHERE val <   > 30 ORDER BY id ASC;")
	if err != nil || len(res.Rows) != 4 {
		t.Fatalf("<   > failed: %v (rows: %v)", err, res.Rows)
	}
}

func TestQuantifiedOperators_Subquery(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE items (id INT PRIMARY KEY, price INT);")
	coord.ExecuteSQL("INSERT INTO items VALUES (1, 10), (2, 20), (3, 30), (4, 40), (5, 50);")

	coord.ExecuteSQL("CREATE TABLE budget (limit_val INT);")
	coord.ExecuteSQL("INSERT INTO budget VALUES (25), (35);")

	// 1. col > ALL (25, 35) -> price > 35 -> items 4 (40) and 5 (50)
	res, err := coord.ExecuteSQL("SELECT id FROM items WHERE price > ALL (SELECT limit_val FROM budget) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("> ALL failed: %v", err)
	}
	if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "4" || fmt.Sprintf("%v", res.Rows[1]["id"]) != "5" {
		t.Fatalf("> ALL expected ids 4, 5, got: %v", res.Rows)
	}

	// 2. col < ALL (25, 35) -> price < 25 -> items 1 (10) and 2 (20)
	res, err = coord.ExecuteSQL("SELECT id FROM items WHERE price < ALL (SELECT limit_val FROM budget) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("< ALL failed: %v", err)
	}
	if len(res.Rows) != 2 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "1" || fmt.Sprintf("%v", res.Rows[1]["id"]) != "2" {
		t.Fatalf("< ALL expected ids 1, 2, got: %v", res.Rows)
	}

	// 3. col > ANY (25, 35) -> price > 25 -> items 3 (30), 4 (40), 5 (50)
	res, err = coord.ExecuteSQL("SELECT id FROM items WHERE price > ANY (SELECT limit_val FROM budget) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("> ANY failed: %v", err)
	}
	if len(res.Rows) != 3 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "3" {
		t.Fatalf("> ANY expected ids 3, 4, 5, got: %v", res.Rows)
	}

	// 4. col < SOME (25, 35) -> price < 35 -> items 1 (10), 2 (20), 3 (30)
	res, err = coord.ExecuteSQL("SELECT id FROM items WHERE price < SOME (SELECT limit_val FROM budget) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("< SOME failed: %v", err)
	}
	if len(res.Rows) != 3 || fmt.Sprintf("%v", res.Rows[2]["id"]) != "3" {
		t.Fatalf("< SOME expected ids 1, 2, 3, got: %v", res.Rows)
	}

	// 5. col = ANY (25, 35) -> exact IN -> no match (prices are 10, 20, 30, 40, 50)
	res, err = coord.ExecuteSQL("SELECT id FROM items WHERE price = ANY (SELECT limit_val FROM budget);")
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("= ANY expected 0 rows, got: %v", res.Rows)
	}

	// Add matching value 20 to budget
	coord.ExecuteSQL("INSERT INTO budget VALUES (20);")
	res, err = coord.ExecuteSQL("SELECT id FROM items WHERE price = ANY (SELECT limit_val FROM budget);")
	if err != nil || len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "2" {
		t.Fatalf("= ANY expected id 2, got: %v", res.Rows)
	}

	// 6. col <> ALL (25, 35, 20) -> NOT IN (25, 35, 20) -> items 1 (10), 3 (30), 4 (40), 5 (50)
	res, err = coord.ExecuteSQL("SELECT id FROM items WHERE price <> ALL (SELECT limit_val FROM budget) ORDER BY id ASC;")
	if err != nil || len(res.Rows) != 4 {
		t.Fatalf("<> ALL expected 4 rows, got: %v", res.Rows)
	}
}

func TestQuantifiedOperators_EmptySetVacuousTruth(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE data_tbl (id INT PRIMARY KEY, score INT);")
	coord.ExecuteSQL("INSERT INTO data_tbl VALUES (1, 100), (2, 200);")

	coord.ExecuteSQL("CREATE TABLE empty_tbl (val INT);")

	// In first-order logic and SQL standard:
	// Forall x in empty_set, P(x) is VACUOUSLY TRUE -> score > ALL (empty) returns ALL rows
	res, err := coord.ExecuteSQL("SELECT id FROM data_tbl WHERE score > ALL (SELECT val FROM empty_tbl) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("> ALL on empty set failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("> ALL on empty set expected 2 rows (vacuous truth), got: %d (%v)", len(res.Rows), res.Rows)
	}

	// Exists x in empty_set, P(x) is FALSE -> score > ANY (empty) returns 0 rows
	res, err = coord.ExecuteSQL("SELECT id FROM data_tbl WHERE score > ANY (SELECT val FROM empty_tbl);")
	if err != nil || !res.Success {
		t.Fatalf("> ANY on empty set failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("> ANY on empty set expected 0 rows, got: %d (%v)", len(res.Rows), res.Rows)
	}
}

func TestQuantifiedOperators_NotEqualAny_SQL3VL_NullExclusion(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	coord.ExecuteSQL("CREATE TABLE test_3vl (id INT PRIMARY KEY, val INT);")
	coord.ExecuteSQL("INSERT INTO test_3vl VALUES (1, 10), (2, 20), (3, NULL);")

	coord.ExecuteSQL("CREATE TABLE sub_distinct (x INT);")
	coord.ExecuteSQL("INSERT INTO sub_distinct VALUES (10), (20);")

	// In SQL 3-valued logic:
	// val=10: (10 <> 10) OR (10 <> 20) -> FALSE OR TRUE -> TRUE (Included)
	// val=20: (20 <> 10) OR (20 <> 20) -> TRUE OR FALSE -> TRUE (Included)
	// val=NULL: (NULL <> 10) OR (NULL <> 20) -> UNKNOWN OR UNKNOWN -> UNKNOWN (EXCLUDED from WHERE!)
	// Only rows 1 and 2 must be returned. Row 3 (NULL) must be excluded!
	res, err := coord.ExecuteSQL("SELECT id FROM test_3vl WHERE val <> ANY (SELECT x FROM sub_distinct) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("<> ANY query failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("<> ANY expected 2 rows (excluding NULL row 3), got %d rows: %v", len(res.Rows), res.Rows)
	}

	// Case 2: Subquery has single unique non-null value (10)
	coord.ExecuteSQL("CREATE TABLE sub_single (x INT);")
	coord.ExecuteSQL("INSERT INTO sub_single VALUES (10);")
	res, err = coord.ExecuteSQL("SELECT id FROM test_3vl WHERE val <> ANY (SELECT x FROM sub_single) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("<> ANY single failed: %v", err)
	}
	// val=10 -> 10<>10 is FALSE. val=20 -> 20<>10 is TRUE. val=NULL -> NULL<>10 is UNKNOWN.
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "2" {
		t.Fatalf("<> ANY single expected only id 2, got: %v", res.Rows)
	}

	// Case 3: Subquery has all NULL values
	coord.ExecuteSQL("CREATE TABLE sub_nulls (x INT);")
	coord.ExecuteSQL("INSERT INTO sub_nulls VALUES (NULL);")
	res, err = coord.ExecuteSQL("SELECT id FROM test_3vl WHERE val <> ANY (SELECT x FROM sub_nulls);")
	if err != nil || !res.Success {
		t.Fatalf("<> ANY all nulls failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("<> ANY all nulls expected 0 rows, got: %v", res.Rows)
	}

	// Case 4: Subquery has (10, NULL)
	coord.ExecuteSQL("INSERT INTO sub_nulls VALUES (10);") // sub_nulls now has (NULL, 10)
	res, err = coord.ExecuteSQL("SELECT id FROM test_3vl WHERE val <> ANY (SELECT x FROM sub_nulls) ORDER BY id ASC;")
	if err != nil || !res.Success {
		t.Fatalf("<> ANY mixed nulls failed: %v", err)
	}
	// val=10: (10<>NULL) OR (10<>10) -> UNKNOWN OR FALSE -> UNKNOWN (Excluded)
	// val=20: (20<>NULL) OR (20<>10) -> UNKNOWN OR TRUE -> TRUE (Included)
	// val=NULL: (NULL<>NULL) OR (NULL<>10) -> UNKNOWN OR UNKNOWN -> UNKNOWN (Excluded)
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "2" {
		t.Fatalf("<> ANY mixed nulls expected only id 2, got: %v", res.Rows)
	}
}
