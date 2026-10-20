package proc

import (
	"strings"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

func TestGroup5_StringBuiltins_NullPropagation(t *testing.T) {
	nullStr := storage.NullValue(storage.TypeVarChar)
	nullInt := storage.NullValue(storage.TypeInt)

	tests := []struct {
		fn   string
		args []storage.Value
	}{
		{"UPPER", []storage.Value{nullStr}},
		{"LOWER", []storage.Value{nullStr}},
		{"TRIM", []storage.Value{nullStr}},
		{"TRIM", []storage.Value{storage.StringValue(" "), nullStr}},
		{"TRIM", []storage.Value{nullStr, storage.StringValue("hello")}},
		{"LTRIM", []storage.Value{nullStr}},
		{"RTRIM", []storage.Value{nullStr}},
		{"LEFT", []storage.Value{nullStr, storage.IntValue(2)}},
		{"LEFT", []storage.Value{storage.StringValue("abc"), nullInt}},
		{"RIGHT", []storage.Value{nullStr, storage.IntValue(2)}},
		{"RIGHT", []storage.Value{storage.StringValue("abc"), nullInt}},
		{"SUBSTRING", []storage.Value{nullStr, storage.IntValue(1), storage.IntValue(2)}},
		{"SUBSTRING", []storage.Value{storage.StringValue("abc"), nullInt, storage.IntValue(2)}},
		{"SUBSTRING", []storage.Value{storage.StringValue("abc"), storage.IntValue(1), nullInt}},
		{"REPLACE", []storage.Value{nullStr, storage.StringValue("a"), storage.StringValue("b")}},
		{"REPLACE", []storage.Value{storage.StringValue("abc"), nullStr, storage.StringValue("b")}},
		{"REPLACE", []storage.Value{storage.StringValue("abc"), storage.StringValue("a"), nullStr}},
		{"REVERSE", []storage.Value{nullStr}},
		{"CHARINDEX", []storage.Value{nullStr, storage.StringValue("abc")}},
		{"CHARINDEX", []storage.Value{storage.StringValue("a"), nullStr}},
		{"CHARINDEX", []storage.Value{storage.StringValue("a"), storage.StringValue("abc"), nullInt}},
		{"STRPOS", []storage.Value{nullStr, storage.StringValue("abc")}},
		{"STRPOS", []storage.Value{storage.StringValue("abc"), nullStr}},
		{"LEN", []storage.Value{nullStr}},
		{"SPACE", []storage.Value{nullInt}},
		{"REPLICATE", []storage.Value{nullStr, storage.IntValue(3)}},
		{"REPLICATE", []storage.Value{storage.StringValue("a"), nullInt}},
	}

	for _, tc := range tests {
		res := evalStringBuiltin(tc.fn, tc.args)
		if !res.IsNull {
			t.Errorf("expected %s to return NULL on null arguments, got: %+v", tc.fn, res)
		}
	}
}

func TestGroup5_DateBuiltins_NullPropagationAndOffset(t *testing.T) {
	nullStr := storage.NullValue(storage.TypeVarChar)
	nullInt := storage.NullValue(storage.TypeInt)
	nowVal := storage.DateTimeValue(time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC))

	// DATEADD with null args must return NULL
	r1 := evalDateBuiltin("DATEADD", []storage.Value{storage.StringValue("day"), nullInt, nowVal})
	if !r1.IsNull {
		t.Errorf("DATEADD with null increment should return NULL, got: %+v", r1)
	}

	r2 := evalDateBuiltin("DATEADD", []storage.Value{storage.StringValue("day"), storage.IntValue(1), nullStr})
	if !r2.IsNull {
		t.Errorf("DATEADD with null date should return NULL, got: %+v", r2)
	}

	// EOMONTH with null month_to_add must return NULL
	r3 := evalDateBuiltin("EOMONTH", []storage.Value{nowVal, nullInt})
	if !r3.IsNull {
		t.Errorf("EOMONTH with null month_to_add should return NULL, got: %+v", r3)
	}

	// TODATETIMEOFFSET with integer minutes offset
	// -120 minutes = -02:00
	r4 := evalDateBuiltin("TODATETIMEOFFSET", []storage.Value{nowVal, storage.IntValue(-120)})
	if r4.IsNull || !strings.HasSuffix(r4.SVal, "-02:00") {
		t.Errorf("expected -02:00 offset from -120 minutes, got: %s", r4.SVal)
	}

	// +330 minutes = +05:30 (India Standard Time)
	r5 := evalDateBuiltin("TODATETIMEOFFSET", []storage.Value{nowVal, storage.IntValue(330)})
	if r5.IsNull || !strings.HasSuffix(r5.SVal, "+05:30") {
		t.Errorf("expected +05:30 offset from +330 minutes, got: %s", r5.SVal)
	}

	// Standard string offset "+09:00" (KST)
	r6 := evalDateBuiltin("TODATETIMEOFFSET", []storage.Value{nowVal, storage.StringValue("+09:00")})
	if r6.IsNull || !strings.HasSuffix(r6.SVal, "+09:00") {
		t.Errorf("expected +09:00 offset from string, got: %s", r6.SVal)
	}
}

func TestGroup5_EvalNeqNullThreeValuedLogic(t *testing.T) {
	// TokenNeq with NULL must return (nil, nil) representing UNKNOWN
	val1, err1 := evalRowBinaryOp(TokenNeq, nil, "abc")
	if err1 != nil || val1 != nil {
		t.Errorf("expected (nil, nil) for NULL <> 'abc', got: %v (err: %v)", val1, err1)
	}

	val2, err2 := evalRowBinaryOp(TokenNeq, "abc", nil)
	if err2 != nil || val2 != nil {
		t.Errorf("expected (nil, nil) for 'abc' <> NULL, got: %v (err: %v)", val2, err2)
	}

	val3, err3 := evalRowBinaryOp(TokenNeq, nil, nil)
	if err3 != nil || val3 != nil {
		t.Errorf("expected (nil, nil) for NULL <> NULL, got: %v (err: %v)", val3, err3)
	}

	// Non-null comparisons
	val4, err4 := evalRowBinaryOp(TokenNeq, "abc", "xyz")
	if err4 != nil || val4 != true {
		t.Errorf("expected true for 'abc' <> 'xyz', got: %v", val4)
	}

	val5, err5 := evalRowBinaryOp(TokenNeq, "abc", "abc")
	if err5 != nil || val5 != false {
		t.Errorf("expected false for 'abc' <> 'abc', got: %v", val5)
	}
}
