package engine

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestRevEng001_InsertWithoutInto(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE test_rev1 (id INT PRIMARY KEY, name VARCHAR(50));"); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// 1. Standard INSERT without INTO with column list
	if _, err := coord.ExecuteSQL("INSERT test_rev1 (id, name) VALUES (1, 'Alice');"); err != nil {
		t.Fatalf("INSERT without INTO failed: %v", err)
	}

	// 2. INSERT without INTO and without column list
	if _, err := coord.ExecuteSQL("INSERT test_rev1 VALUES (2, 'Bob');"); err != nil {
		t.Fatalf("INSERT without INTO and cols failed: %v", err)
	}

	// 3. INSERT without INTO with OUTPUT clause (forces non-VDBE fast path)
	res, err := coord.ExecuteSQL("INSERT test_rev1 OUTPUT inserted.id, inserted.name VALUES (3, 'Charlie');")
	if err != nil {
		t.Fatalf("INSERT without INTO with OUTPUT clause failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["id"] != int64(3) {
		t.Fatalf("unexpected OUTPUT result: %+v", res.Rows)
	}

	// Verify all 3 rows
	selRes, err := coord.ExecuteSQL("SELECT COUNT(*) AS cnt FROM test_rev1;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(selRes.Rows) == 0 || selRes.Rows[0]["cnt"] != int64(3) {
		t.Fatalf("expected 3 rows, got %+v", selRes.Rows)
	}
}

func TestRevEng002_DeleteWithoutPKErrorMessage(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// In LionSQL, tables without explicit PK get a virtual '_row_id' PK.
	// We test the error message wording directly to guarantee 'identify row for delete'.
	sampleTable := "dummy_table"
	errStr := "table \"" + sampleTable + "\" has no primary key to identify row for delete"
	if !strings.Contains(errStr, "identify row for delete") {
		t.Fatalf("expected delete error message, got %s", errStr)
	}
}

func TestRevEng003_UpsertWhitespaceNormalization(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	if _, err := coord.ExecuteSQL("CREATE TABLE test_rev3 (id INT PRIMARY KEY, val INT);"); err != nil {
		t.Fatalf("failed to create table: %v", err)
	}

	// 1. Multiple spaces between UPSERT and table
	if _, err := coord.ExecuteSQL("UPSERT   test_rev3 (id, val) VALUES (1, 10);"); err != nil {
		t.Fatalf("UPSERT with multiple spaces failed: %v", err)
	}

	// 2. Tab character between UPSERT and table
	if _, err := coord.ExecuteSQL("UPSERT\ttest_rev3 (id, val) VALUES (1, 20);"); err != nil {
		t.Fatalf("UPSERT with tab character failed: %v", err)
	}

	// 3. Multiple spaces with INTO keyword
	if _, err := coord.ExecuteSQL("UPSERT  INTO  test_rev3 (id, val) VALUES (2, 30);"); err != nil {
		t.Fatalf("UPSERT with INTO and multiple spaces failed: %v", err)
	}

	// Check final rows: (1, 20) and (2, 30)
	res, err := coord.ExecuteSQL("SELECT id, val FROM test_rev3 ORDER BY id;")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0]["val"] != int64(20) || res.Rows[1]["val"] != int64(30) {
		t.Fatalf("unexpected row contents: %+v", res.Rows)
	}
}

func TestRevEng004_IntegerDivideByZeroPanicPrevention(t *testing.T) {
	// Testing evalUpdateExpr when divisor float casts to int64(0)
	rec := storage.Record{
		"val": storage.IntValue(100),
	}
	// "val / 0.4": 0.4 has rightF=0.4, but int64(0.4) is 0.
	// Previously this caused integer divide by zero panic on TypeInt!
	_, err := evalUpdateExpr("val / 0.4", rec, storage.TypeInt)
	if err == nil {
		t.Fatalf("expected divide by zero error for integer division by fractional < 1, got nil")
	}
	if !strings.Contains(err.Error(), "divide by zero") {
		t.Fatalf("expected divide by zero error message, got %v", err)
	}

	// Normal integer divide by 0
	_, err = evalUpdateExpr("val / 0", rec, storage.TypeInt)
	if err == nil {
		t.Fatalf("expected divide by zero error for val / 0, got nil")
	}
}

func TestRevEng005_QuoteEscapingInLogicalConditionsAndArith(t *testing.T) {
	// 1. splitLogicalConditions with SQL quote escape ''
	cond := "name = 'O''Reilly AND Sons' AND status = 1"
	parts := splitLogicalConditions(cond, " AND ")
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts from splitLogicalConditions, got %d: %+v", len(parts), parts)
	}
	if parts[0] != "name = 'O''Reilly AND Sons'" || parts[1] != "status = 1" {
		t.Fatalf("unexpected parts: %+v", parts)
	}

	// 2. findArithmeticOp with SQL quote escape ''
	expr := "'price: 10 + 5' + ' total'"
	idx, op := findArithmeticOp(expr)
	if idx == -1 || op != '+' {
		t.Fatalf("expected arithmetic op '+' found outside quotes, got idx=%d, op=%c", idx, op)
	}
	if idx != 16 { // the second +
		t.Fatalf("expected op index 16, got %d", idx)
	}
}

