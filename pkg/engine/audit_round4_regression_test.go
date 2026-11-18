package engine

import (
	"fmt"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestAuditRound4_BracketStringAndHashJoin(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// Bug 24 (B-1): String comparison with brackets must not strip brackets
	if _, err := coord.ExecuteSQL(`
		CREATE TABLE _t_bracket (id INT PRIMARY KEY, tag VARCHAR(20));
		INSERT INTO _t_bracket VALUES (1, '[ADMIN]'), (2, 'ADMIN');
	`); err != nil {
		t.Fatalf("setup _t_bracket failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT id, tag FROM _t_bracket WHERE tag = 'ADMIN';")
	if err != nil {
		t.Fatalf("query _t_bracket failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row for tag = 'ADMIN', got %d: %v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["id"]) != "2" || fmt.Sprintf("%v", res.Rows[0]["tag"]) != "ADMIN" {
		t.Errorf("unexpected row: %v", res.Rows[0])
	}

	// Bug 33 (B-2): Hash Join must not strip brackets in join keys
	if _, err := coord.ExecuteSQL(`
		CREATE TABLE _t_hj_l (id INT PRIMARY KEY, code VARCHAR(20));
		CREATE TABLE _t_hj_r (id INT PRIMARY KEY, code VARCHAR(20));
		INSERT INTO _t_hj_l VALUES (1, '[code1]'), (2, 'code1');
		INSERT INTO _t_hj_r VALUES (10, 'code1');
	`); err != nil {
		t.Fatalf("setup hash join tables failed: %v", err)
	}

	resHJ, err := coord.ExecuteSQL(`
		SELECT l.id AS lid, l.code AS lcode, r.id AS rid
		FROM _t_hj_l l
		JOIN _t_hj_r r ON l.code = r.code;
	`)
	if err != nil {
		t.Fatalf("hash join query failed: %v", err)
	}
	if len(resHJ.Rows) != 1 {
		t.Fatalf("expected 1 joined row, got %d: %v", len(resHJ.Rows), resHJ.Rows)
	}
	if fmt.Sprintf("%v", resHJ.Rows[0]["lid"]) != "2" {
		t.Errorf("expected lid=2, got %v", resHJ.Rows[0]["lid"])
	}
}

func TestAuditRound4_StoredProcNull3VL(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// Bug 25 (C-1): Stored procedure IF @a = @b when both are NULL must take ELSE branch
	if _, err := coord.ExecuteSQL(`
		CREATE PROCEDURE _sp_null_test()
		AS
		BEGIN
			DECLARE @a INT = NULL;
			DECLARE @b INT = NULL;
			IF @a = @b
				SELECT 'BUG: EQUAL' AS res;
			ELSE
				SELECT 'CORRECT: NOT EQUAL' AS res;
		END;
	`); err != nil {
		t.Fatalf("create procedure failed: %v", err)
	}

	res, err := coord.ExecuteSQL("CALL _sp_null_test();")
	if err != nil {
		t.Fatalf("call procedure failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["res"]) != "CORRECT: NOT EQUAL" {
		t.Fatalf("expected 'CORRECT: NOT EQUAL', got: %v", res.Rows)
	}

	// Also verify IF @a <> 10 when @a is NULL takes ELSE branch
	if _, err := coord.ExecuteSQL(`
		CREATE PROCEDURE _sp_null_neq_test()
		AS
		BEGIN
			DECLARE @a INT = NULL;
			IF @a <> 10
				SELECT 'BUG: NEQ TRUE' AS res;
			ELSE
				SELECT 'CORRECT: NEQ UNKNOWN' AS res;
		END;
	`); err != nil {
		t.Fatalf("create procedure neq failed: %v", err)
	}

	resNeq, err := coord.ExecuteSQL("CALL _sp_null_neq_test();")
	if err != nil {
		t.Fatalf("call procedure neq failed: %v", err)
	}
	if len(resNeq.Rows) != 1 || fmt.Sprintf("%v", resNeq.Rows[0]["res"]) != "CORRECT: NEQ UNKNOWN" {
		t.Fatalf("expected 'CORRECT: NEQ UNKNOWN', got: %v", resNeq.Rows)
	}
}

func TestAuditRound4_DivZeroAndOverflowAndDateDiff(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// Bug 26 (D-1): Division by zero must return explicit error
	_, errDiv := coord.ExecuteSQL("SELECT 10 / 0 AS div_zero;")
	if errDiv == nil {
		t.Fatalf("expected division by zero error for SELECT 10 / 0, got nil")
	}
	if !strings.Contains(strings.ToLower(errDiv.Error()), "zero") {
		t.Errorf("expected divide by zero error message, got: %v", errDiv)
	}

	// Bug 28 (D-2): 64-bit integer overflow must return explicit error
	_, errMul := coord.ExecuteSQL("SELECT 9223372036854775807 * 2 AS overflow_mul;")
	if errMul == nil {
		t.Fatalf("expected overflow error for 9223372036854775807 * 2, got nil")
	}
	if !strings.Contains(strings.ToLower(errMul.Error()), "overflow") && !strings.Contains(strings.ToLower(errMul.Error()), "out of range") {
		t.Errorf("expected overflow error message, got: %v", errMul)
	}

	_, errAdd := coord.ExecuteSQL("SELECT 9223372036854775807 + 1 AS overflow_add;")
	if errAdd == nil {
		t.Fatalf("expected overflow error for 9223372036854775807 + 1, got nil")
	}

	// Bug 27 (D-4): T-SQL DATEDIFF boundary crossing
	resDiff, err := coord.ExecuteSQL(`
		SELECT 
			DATEDIFF(week, '2023-01-07', '2023-01-08') AS diff_week,
			DATEDIFF(hour, '2023-01-01 23:59:59', '2023-01-02 00:00:01') AS diff_hour,
			DATEDIFF(minute, '2023-01-01 12:00:59', '2023-01-01 12:01:01') AS diff_minute;
	`)
	if err != nil {
		t.Fatalf("DATEDIFF query failed: %v", err)
	}
	if len(resDiff.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(resDiff.Rows))
	}
	r := resDiff.Rows[0]
	if fmt.Sprintf("%v", r["diff_week"]) != "1" || fmt.Sprintf("%v", r["diff_hour"]) != "1" || fmt.Sprintf("%v", r["diff_minute"]) != "1" {
		t.Errorf("expected (1, 1, 1) for DATEDIFF boundaries, got (%v, %v, %v)", r["diff_week"], r["diff_hour"], r["diff_minute"])
	}
}

func TestAuditRound4_MergeIntoCompositePK(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// Bug 31 (A-5): MERGE INTO on composite PK table
	if _, err := coord.ExecuteSQL(`
		CREATE TABLE _t_m_target (k1 INT, k2 INT, val INT, PRIMARY KEY (k1, k2));
		CREATE TABLE _t_m_source (k1 INT, k2 INT, val INT);
		INSERT INTO _t_m_target VALUES (1, 10, 100), (1, 20, 200);
		INSERT INTO _t_m_source VALUES (1, 20, 999), (1, 30, 300);
	`); err != nil {
		t.Fatalf("setup MERGE composite tables failed: %v", err)
	}

	_, err := coord.ExecuteSQL(`
		MERGE INTO _t_m_target t
		USING _t_m_source s ON t.k1 = s.k1 AND t.k2 = s.k2
		WHEN MATCHED THEN UPDATE SET val = s.val
		WHEN NOT MATCHED THEN INSERT (k1, k2, val) VALUES (s.k1, s.k2, s.val)
		WHEN NOT MATCHED BY SOURCE THEN DELETE;
	`)
	if err != nil {
		t.Fatalf("MERGE INTO composite PK failed: %v", err)
	}

	res, err := coord.ExecuteSQL("SELECT k1, k2, val FROM _t_m_target ORDER BY k1, k2;")
	if err != nil {
		t.Fatalf("select target failed: %v", err)
	}
	// (1, 10) should be deleted (not matched by source)
	// (1, 20) should be updated to 999
	// (1, 30) should be inserted with 300
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows in target after MERGE, got %d: %v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["k2"]) != "20" || fmt.Sprintf("%v", res.Rows[0]["val"]) != "999" {
		t.Errorf("expected first row (1, 20, 999), got %v", res.Rows[0])
	}
	if fmt.Sprintf("%v", res.Rows[1]["k2"]) != "30" || fmt.Sprintf("%v", res.Rows[1]["val"]) != "300" {
		t.Errorf("expected second row (1, 30, 300), got %v", res.Rows[1])
	}
}
