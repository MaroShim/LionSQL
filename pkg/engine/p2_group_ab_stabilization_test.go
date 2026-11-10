package engine_test

import (
	"fmt"
	"math"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/execution"
	"lionsql/pkg/proc"
	"lionsql/pkg/sqlutil"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestP2_GroupA_Scope017_FastRandN(t *testing.T) {
	for i := 0; i < 1000; i++ {
		v := storage.FastRandN(10)
		if v < 0 || v >= 10 {
			t.Fatalf("FastRandN(10) returned out-of-range value: %d", v)
		}
	}
	if v := storage.FastRandN(0); v != 0 {
		t.Fatalf("FastRandN(0) must return 0, got %d", v)
	}
	if v := storage.FastRandN(-5); v != 0 {
		t.Fatalf("FastRandN(-5) must return 0, got %d", v)
	}
}

func TestP2_GroupA_Scope056_LexerColClamping(t *testing.T) {
	lex := proc.NewLexer("\n  SELECT 1;")
	for {
		tok := lex.NextToken()
		if tok.Col < 1 {
			t.Fatalf("Token %v has invalid col %d < 1", tok, tok.Col)
		}
		if tok.Line < 1 {
			t.Fatalf("Token %v has invalid line %d < 1", tok, tok.Line)
		}
		if tok.Type == proc.TokenEOF {
			break
		}
	}
}

func TestP2_GroupA_Scope071_BuiltinMathModOverflow(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// MinInt64 % -1 in SQL
	res, err := coord.ExecuteSQL("SELECT MOD(-9223372036854775808, -1) AS m;")
	if err != nil || !res.Success {
		t.Fatalf("MOD overflow query failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["m"]) != "0" {
		t.Fatalf("expected 0, got: %v", res.Rows)
	}
}

func TestP2_GroupA_Scope095_ParseMemSizeOverflow(t *testing.T) {
	// Normal size
	b, err := execution.ParseMemSize("128MB")
	if err != nil || b != 128*1024*1024 {
		t.Fatalf("expected 128MB, got %d, err: %v", b, err)
	}

	// Giant overflow size
	b, err = execution.ParseMemSize("9999999999999999999GB")
	if err != nil {
		t.Fatalf("unexpected error on huge memory size: %v", err)
	}
	if b <= 0 {
		t.Fatalf("expected positive clamped memory size, got %d", b)
	}

	// Negative or empty
	b, _ = execution.ParseMemSize("-10MB")
	if b <= 0 {
		t.Fatalf("expected positive default for negative size, got %d", b)
	}
}

func TestP2_GroupA_Scope114_FindMatchingParenNegativeDepth(t *testing.T) {
	// Bad string where closing paren appears before opening
	s := "foo)bar(baz)"
	idx := sqlutil.FindMatchingParen(s, 7) // '(' is at index 7
	if idx != 11 {
		t.Fatalf("expected index 11, got %d", idx)
	}

	// Calling on unmatched closing paren should return -1
	idx2 := sqlutil.FindMatchingParen("))", 0)
	if idx2 != -1 {
		t.Fatalf("expected -1, got %d", idx2)
	}
}

func TestP2_GroupA_Scope052_StringSplitEmptyDelim(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	res, err := coord.ExecuteSQL("SELECT * FROM STRING_SPLIT('a,b,c', '');")
	if err == nil && res != nil && res.Success {
		t.Fatalf("expected error on empty delimiter, got success")
	}
}

func TestP2_GroupB_Scope070_SubstringNegativeStart(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// T-SQL: SUBSTRING('12345', -2, 5) -> '12'
	res, err := coord.ExecuteSQL("SELECT SUBSTRING('12345', -2, 5) AS s1, SUBSTRING('12345', 0, 2) AS s2, SUBSTRING('12345', -5, 2) AS s3;")
	if err != nil || !res.Success {
		t.Fatalf("SUBSTRING query failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["s1"]) != "12" {
		t.Errorf("expected s1 = '12', got %v", res.Rows[0]["s1"])
	}
	if fmt.Sprintf("%v", res.Rows[0]["s2"]) != "1" {
		t.Errorf("expected s2 = '1', got %v", res.Rows[0]["s2"])
	}
	if fmt.Sprintf("%v", res.Rows[0]["s3"]) != "" {
		t.Errorf("expected s3 = '', got %v", res.Rows[0]["s3"])
	}
}

func TestP2_GroupB_Scope073_ServerPropertyNull(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	res, err := coord.ExecuteSQL("SELECT SERVERPROPERTY('NonExistentProp') AS prop;")
	if err != nil || !res.Success {
		t.Fatalf("SERVERPROPERTY query failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["prop"] != nil {
		t.Fatalf("expected NULL for unknown server property, got: %v", res.Rows[0]["prop"])
	}
}

func TestP2_GroupB_Scope051_EvaluatorMinInt64Overflow(t *testing.T) {
	left := storage.IntValue(math.MinInt64)
	right := storage.IntValue(-1)

	// Division overflow
	_, err := engine.EvalBinaryOp('/', left, right, storage.TypeInt)
	if err == nil {
		t.Fatalf("expected error on MinInt64 / -1, got nil")
	}

	// Modulo overflow
	modVal, err := engine.EvalBinaryOp('%', left, right, storage.TypeInt)
	if err != nil {
		t.Fatalf("unexpected error on MinInt64 %% -1: %v", err)
	}
	if modVal.IVal != 0 {
		t.Fatalf("expected 0 on MinInt64 %% -1, got %d", modVal.IVal)
	}
}

func TestP2_GroupB_Scope021_InvalidDateStringConversion(t *testing.T) {
	d := storage.DateValueFromString("not-a-valid-date")
	if !d.IsNull {
		t.Fatalf("expected IsNull for invalid date string, got %v", d)
	}
}

func TestAuditIncremental_TableVarRedeclarationDropOldTable(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	spSQL := `
CREATE PROCEDURE ProcRedeclareTableVar
AS
BEGIN
    DECLARE @counter INT = 0;
    WHILE @counter < 3
    BEGIN
        DECLARE @tmp TABLE (id INT, val VARCHAR(20));
        INSERT INTO @tmp VALUES (@counter, 'test');
        SET @counter = @counter + 1;
    END;
    SELECT id, val FROM @tmp;
END;
`
	_, err := coord.ExecuteSQL(spSQL)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	res, err := coord.ExecuteSQL("EXEC ProcRedeclareTableVar;")
	if err != nil || !res.Success {
		t.Fatalf("EXEC ProcRedeclareTableVar failed: %v", err)
	}
	if len(res.Rows) != 1 || fmt.Sprintf("%v", res.Rows[0]["id"]) != "2" {
		t.Fatalf("expected 1 row with id=2, got: %v", res.Rows)
	}
}

func TestAuditIncremental_QuantifiedSubqueryNull3VL(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE nums (val INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE nums failed: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO nums VALUES (10), (NULL), (20);")
	if err != nil {
		t.Fatalf("INSERT nums failed: %v", err)
	}

	_, err = coord.ExecuteSQL("CREATE TABLE candidates (c INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE candidates failed: %v", err)
	}
	_, err = coord.ExecuteSQL("INSERT INTO candidates VALUES (5), (15), (25), (100);")
	if err != nil {
		t.Fatalf("INSERT candidates failed: %v", err)
	}

	// SQL 3VL: c > ALL (10, NULL, 20) -> for any c, comparison with NULL is UNKNOWN,
	// and AND of any booleans with UNKNOWN is either FALSE or UNKNOWN, never TRUE.
	// Therefore, WHERE clause must return 0 rows!
	res, err := coord.ExecuteSQL("SELECT c FROM candidates WHERE c > ALL (SELECT val FROM nums);")
	if err != nil || !res.Success {
		t.Fatalf("SELECT with > ALL query failed: %v", err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows due to 3VL UNKNOWN result, but got %d rows: %v", len(res.Rows), res.Rows)
	}
}
