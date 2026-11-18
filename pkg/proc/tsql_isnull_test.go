package proc_test

import (
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/storage"
)

func TestTSQL_ISNULL_Truncation(t *testing.T) {
	// ISNULL(CAST(NULL AS VARCHAR(3)), 'abcdefgh') should return 'abc'
	fn := &proc.FunctionCallExpr{
		Name: "ISNULL",
		Args: []proc.Expr{
			&proc.CastExpr{
				Expr:       &proc.LiteralExpr{Val: storage.NullValue(storage.TypeVarChar)},
				TargetType: storage.TypeVarChar,
				Precision:  3,
			},
			&proc.LiteralExpr{
				Val: storage.StringValue("abcdefgh"),
			},
		},
	}

	res, err := proc.EvalRowExpr(fn, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	strVal, ok := res.(string)
	if !ok {
		t.Fatalf("expected string, got %T (%v)", res, res)
	}
	if strVal != "abc" {
		t.Errorf("expected 'abc', got %q", strVal)
	}

	// COALESCE(CAST(NULL AS VARCHAR(3)), 'abcdefgh') should NOT truncate, returns 'abcdefgh'
	fnCoalesce := &proc.FunctionCallExpr{
		Name: "COALESCE",
		Args: []proc.Expr{
			&proc.CastExpr{
				Expr:       &proc.LiteralExpr{Val: storage.NullValue(storage.TypeVarChar)},
				TargetType: storage.TypeVarChar,
				Precision:  3,
			},
			&proc.LiteralExpr{
				Val: storage.StringValue("abcdefgh"),
			},
		},
	}

	resCoalesce, err := proc.EvalRowExpr(fnCoalesce, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	strValCoalesce, ok := resCoalesce.(string)
	if !ok {
		t.Fatalf("expected string, got %T (%v)", resCoalesce, resCoalesce)
	}
	if strValCoalesce != "abcdefgh" {
		t.Errorf("expected 'abcdefgh', got %q", strValCoalesce)
	}

	// ISNULL where first arg is non-null
	fnNonNull := &proc.FunctionCallExpr{
		Name: "ISNULL",
		Args: []proc.Expr{
			&proc.LiteralExpr{
				Val: storage.StringValue("hello"),
			},
			&proc.LiteralExpr{
				Val: storage.StringValue("fallback"),
			},
		},
	}
	resNonNull, err := proc.EvalRowExpr(fnNonNull, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resNonNull != "hello" {
		t.Errorf("expected 'hello', got %v", resNonNull)
	}
}
