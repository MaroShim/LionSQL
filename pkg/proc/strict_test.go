package proc

import (
	"testing"

	"lionsql/pkg/storage"
)

func TestHasAnyNull(t *testing.T) {
	v1 := storage.IntValue(10)
	v2 := storage.StringValue("hello")
	vNull := storage.NullValue(storage.TypeInt)

	if HasAnyNull([]storage.Value{v1, v2}) {
		t.Errorf("expected false for non-null slice")
	}
	if !HasAnyNull([]storage.Value{v1, vNull, v2}) {
		t.Errorf("expected true when slice contains null")
	}
	if !HasNullAt([]storage.Value{v1, vNull}, 1) {
		t.Errorf("expected true for null at index 1")
	}
	if !HasNullAt([]storage.Value{v1}, 5) {
		t.Errorf("expected true for out of range index")
	}
}

func TestStrictDecorators(t *testing.T) {
	// Unary
	doubleFn := StrictUnary(storage.TypeInt, func(a storage.Value) storage.Value {
		return storage.IntValue(a.IVal * 2)
	})

	res := doubleFn([]storage.Value{storage.IntValue(21)})
	if res.IsNull || res.IVal != 42 {
		t.Errorf("expected 42, got %v", res)
	}

	resNull := doubleFn([]storage.Value{storage.NullValue(storage.TypeInt)})
	if !resNull.IsNull || resNull.Type != storage.TypeInt {
		t.Errorf("expected NullValue(TypeInt), got %v", resNull)
	}

	// Binary
	addFn := StrictBinary(storage.TypeInt, func(a, b storage.Value) storage.Value {
		return storage.IntValue(a.IVal + b.IVal)
	})
	resAdd := addFn([]storage.Value{storage.IntValue(10), storage.IntValue(20)})
	if resAdd.IsNull || resAdd.IVal != 30 {
		t.Errorf("expected 30, got %v", resAdd)
	}
	resAddNull := addFn([]storage.Value{storage.IntValue(10), storage.NullValue(storage.TypeInt)})
	if !resAddNull.IsNull {
		t.Errorf("expected null when one arg is null")
	}

	// RegisterStrictScalarFunction
	RegisterStrictScalarFunction("STRICT_TEST_ADD", storage.TypeInt, func(args []storage.Value) storage.Value {
		return storage.IntValue(args[0].IVal + args[1].IVal)
	})
	defer UnregisterScalarFunction("STRICT_TEST_ADD")

	evalRes := EvalBuiltin("STRICT_TEST_ADD", []storage.Value{storage.IntValue(5), storage.IntValue(7)})
	if evalRes.IsNull || evalRes.IVal != 12 {
		t.Errorf("expected 12, got %v", evalRes)
	}
	evalNull := EvalBuiltin("STRICT_TEST_ADD", []storage.Value{storage.IntValue(5), storage.NullValue(storage.TypeInt)})
	if !evalNull.IsNull {
		t.Errorf("expected null return from EvalBuiltin")
	}
}
