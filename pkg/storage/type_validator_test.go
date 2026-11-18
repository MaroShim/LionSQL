package storage_test

import (
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

func TestTypeValidator_VarcharTruncation(t *testing.T) {
	col := &storage.Column{
		Name:      "code",
		Type:      storage.TypeVarChar,
		Precision: 3,
	}

	// 1. Valid length (<= 3)
	val, err := storage.ValidateAndNormalizeValue(col, storage.StringValue("abc"), "test_tbl")
	if err != nil {
		t.Fatalf("expected valid VARCHAR(3) to pass, got error: %v", err)
	}
	if val.String() != "abc" {
		t.Errorf("expected 'abc', got %q", val.String())
	}

	// 2. Over length (> 3) -> Must fail
	_, err = storage.ValidateAndNormalizeValue(col, storage.StringValue("1234567890"), "test_tbl")
	if err == nil {
		t.Fatalf("expected 10-char string into VARCHAR(3) to fail with truncation error, but succeeded")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("expected truncation error message, got: %v", err)
	}
}

func TestTypeValidator_CharPadding(t *testing.T) {
	col := &storage.Column{
		Name:      "tag",
		Type:      storage.TypeVarChar,
		RawType:   "CHAR",
		IsFixed:   true,
		Precision: 5,
	}

	// Storing 'abc' into CHAR(5) must pad with spaces to 5 chars: 'abc  '
	val, err := storage.ValidateAndNormalizeValue(col, storage.StringValue("abc"), "test_tbl")
	if err != nil {
		t.Fatalf("expected CHAR(5) to pass, got error: %v", err)
	}
	if val.String() != "abc  " {
		t.Fatalf("expected 'abc  ' (len 5), got %q (len %d)", val.String(), len(val.String()))
	}
}

func TestTypeValidator_DecimalOverflow(t *testing.T) {
	col := &storage.Column{
		Name:      "price",
		Type:      storage.TypeDecimal,
		Precision: 5,
		Scale:     2,
	}

	// 1. Valid: 999.99 (integer part 999 has 3 digits <= 5-2)
	val, err := storage.ValidateAndNormalizeValue(col, storage.StringValue("999.99"), "test_tbl")
	if err != nil {
		t.Fatalf("expected 999.99 into DECIMAL(5,2) to pass, got error: %v", err)
	}
	if val.Decimal().String() != "999.99" {
		t.Errorf("expected '999.99', got %q", val.Decimal().String())
	}

	// 2. Overflow: 9999999.99 (integer part has 7 digits > 3) -> Must fail
	_, err = storage.ValidateAndNormalizeValue(col, storage.StringValue("9999999.99"), "test_tbl")
	if err == nil {
		t.Fatalf("expected 9999999.99 into DECIMAL(5,2) to fail with overflow, but succeeded")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Errorf("expected overflow error message, got: %v", err)
	}
}

func TestTypeValidator_TinyintRange(t *testing.T) {
	col := &storage.Column{
		Name:    "age",
		Type:    storage.TypeInt,
		RawType: "TINYINT",
	}

	// 1. Valid: 0 and 255
	for _, v := range []int64{0, 100, 255} {
		val, err := storage.ValidateAndNormalizeValue(col, storage.IntValue(v), "test_tbl")
		if err != nil {
			t.Fatalf("expected TINYINT %d to pass, got error: %v", v, err)
		}
		if val.IVal != v {
			t.Errorf("expected %d, got %d", v, val.IVal)
		}
	}

	// 2. Overflow: 999 (> 255) -> Must fail
	_, err := storage.ValidateAndNormalizeValue(col, storage.IntValue(999), "test_tbl")
	if err == nil {
		t.Fatalf("expected 999 into TINYINT to fail with overflow, but succeeded")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Errorf("expected overflow error message, got: %v", err)
	}

	// 3. Underflow: -1 (< 0) -> Must fail
	_, err = storage.ValidateAndNormalizeValue(col, storage.IntValue(-1), "test_tbl")
	if err == nil {
		t.Fatalf("expected -1 into TINYINT to fail with overflow, but succeeded")
	}
}
