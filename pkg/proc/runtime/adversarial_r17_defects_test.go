package runtime_test

import (
	"math"
	"lionsql/pkg/catalog"
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

func TestDEFECT_EvalRowExpr_IsNullExpr_StorageValue(t *testing.T) {
	expr := &proc.IsNullExpr{
		Expr: &proc.ColumnRefExpr{Name: "c1"},
		Not:  false,
	}

	row := map[string]any{
		"c1": storage.NullValue(storage.TypeInt),
	}

	res, err := proc.EvalRowExpr(expr, row, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	b, ok := res.(bool)
	if !ok || !b {
		t.Fatalf("DEFECT 1 CONFIRMED: expected c1 IS NULL to evaluate to true, got: %v (%T)", res, res)
	}
}

func TestDEFECT_EvalRowBinaryOp_NullStorageValue(t *testing.T) {
	expr := &proc.BinaryExpr{
		Op:    proc.TokenPlus,
		Left:  &proc.ColumnRefExpr{Name: "c1"},
		Right: &proc.LiteralExpr{Val: storage.IntValue(10)},
	}

	row := map[string]any{
		"c1": storage.NullValue(storage.TypeInt),
	}

	res, err := proc.EvalRowExpr(expr, row, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res != nil {
		t.Fatalf("DEFECT 2 CONFIRMED: expected NULL + 10 to be nil, got: %v (%T)", res, res)
	}
}

func TestDEFECT_EvalCast_FloatString_To_Int_Error(t *testing.T) {
	sv := storage.StringValue("12.34")
	_, err := proc.EvalCast(sv, storage.TypeInt, 0, 0, 0, false)
	if err == nil {
		t.Fatalf("DEFECT 3 CONFIRMED: expected error converting 12.34 to int, but succeeded")
	}
}

func TestDEFECT_EvalRowBinaryOp_Decimal_Null(t *testing.T) {
	// If left or right is Decimal Null (e.g. storage.NullValue(TypeDecimal)),
	// isDecimalVal(left) is true!
	// But it does not check isNullVal(left) before entering toDecimalVal!

	expr := &proc.BinaryExpr{
		Op:    proc.TokenPlus,
		Left:  &proc.ColumnRefExpr{Name: "c1"},
		Right: &proc.LiteralExpr{Val: storage.DecimalValue("10.5")},
	}

	row := map[string]any{
		"c1": storage.NullValue(storage.TypeDecimal),
	}

	res, err := proc.EvalRowExpr(expr, row, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res != nil {
		t.Fatalf("DEFECT 4 CONFIRMED: expected NULL + 10.5 to be nil, got: %v (%T)", res, res)
	}
}

func TestDEFECT_Sequence_IntegerOverflow(t *testing.T) {
	// If sequence has s.Current = math.MaxInt64 and Increment = 1,
	// and MaxValue is math.MaxInt64,
	// nextVal := s.Current + s.Increment -> OVERFLOW to math.MinInt64!
	// In sequence.go:
	// if nextVal > s.MaxValue { ... }
	// But because nextVal overflowed to negative, nextVal <= s.MaxValue evaluates to true!
	// So it SILENTLY wraps around to MinInt64 without error or cycling!
	seq := &catalog.SequenceDef{
		Name:      "seq_overflow",
		StartWith: math.MaxInt64,
		Increment: 1,
		MinValue:  1,
		MaxValue:  math.MaxInt64,
		Cycle:     false,
	}
	val1, err := seq.Next()
	if err != nil {
		t.Fatalf("first next err: %v", err)
	}
	if val1 != math.MaxInt64 {
		t.Fatalf("expected val1 = MaxInt64, got %d", val1)
	}

	val2, err := seq.Next()
	if err == nil {
		t.Fatalf("DEFECT 5 CONFIRMED: expected sequence exhaustion error on overflow, but got wrapped value: %d", val2)
	}
}
