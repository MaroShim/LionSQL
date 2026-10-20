package storage_test

import (
	"bytes"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

func TestTypeUUID_ParsingAndComparison(t *testing.T) {
	raw := "A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11"
	uVal := storage.UUIDValue(raw)

	expected := "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
	if uVal.String() != expected {
		t.Errorf("expected normalized UUID %q, got %q", expected, uVal.String())
	}
	if uVal.Type != storage.TypeUUID {
		t.Errorf("expected TypeUUID, got %v", uVal.Type)
	}

	// Equality test
	uVal2 := storage.UUIDValue("{a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11}")
	if !uVal.Equal(uVal2) {
		t.Errorf("expected UUIDs to be equal regardless of case and braces")
	}

	// Compare test
	uValSmaller := storage.UUIDValue("00000000-0000-0000-0000-000000000001")
	if uVal.Compare(uValSmaller) <= 0 {
		t.Errorf("expected uVal > uValSmaller")
	}
	if uValSmaller.Compare(uVal) >= 0 {
		t.Errorf("expected uValSmaller < uVal")
	}

	// Invalid UUID handling
	_, err := storage.ParseUUID("not-a-valid-uuid")
	if err == nil {
		t.Errorf("expected error parsing invalid UUID, got nil")
	}
}

func TestTypeBinary_HexAndByteSlice(t *testing.T) {
	rawBytes := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	binVal := storage.BinaryValue(rawBytes)

	if !bytes.Equal(binVal.Bytes(), rawBytes) {
		t.Errorf("expected Bytes %v, got %v", rawBytes, binVal.Bytes())
	}
	if binVal.String() != "0xdeadbeef" {
		t.Errorf("expected hex string 0xdeadbeef, got %s", binVal.String())
	}

	// Hex parsing: 0x... and \x...
	binFromHex1 := storage.BinaryValueFromString("0xDEADBEEF")
	if !binVal.Equal(binFromHex1) {
		t.Errorf("expected 0xDEADBEEF to equal binary value")
	}

	binFromHex2 := storage.BinaryValueFromString("\\xdeadbeef")
	if !binVal.Equal(binFromHex2) {
		t.Errorf("expected \\xdeadbeef to equal binary value")
	}

	// Compare
	binSmaller := storage.BinaryValue([]byte{0x00, 0x01})
	if binVal.Compare(binSmaller) <= 0 {
		t.Errorf("expected binVal > binSmaller")
	}
}

func TestTypeDate_ParsingAndComparison(t *testing.T) {
	dVal := storage.DateValueFromString("2026-10-01")
	if dVal.Type != storage.TypeDate {
		t.Errorf("expected TypeDate, got %v", dVal.Type)
	}
	if dVal.String() != "2026-10-01" {
		t.Errorf("expected 2026-10-01, got %s", dVal.String())
	}

	// Comparison
	dEarlier := storage.DateValueFromString("2026-09-30")
	if dVal.Compare(dEarlier) <= 0 {
		t.Errorf("expected 2026-10-01 > 2026-09-30")
	}
	if dEarlier.Compare(dVal) >= 0 {
		t.Errorf("expected 2026-09-30 < 2026-10-01")
	}

	// Conversion from DateTime
	dt := time.Date(2026, 10, 1, 15, 30, 45, 123456, time.UTC)
	dFromDT := storage.DateValueFromAny(storage.DateTimeValue(dt))
	if !dVal.Equal(dFromDT) {
		t.Errorf("expected Date truncated from DateTime to equal %s, got %s", dVal.String(), dFromDT.String())
	}
}

func TestTypeTime_ParsingAndComparison(t *testing.T) {
	tVal := storage.TimeValueFromString("14:30:15")
	if tVal.Type != storage.TypeTime {
		t.Errorf("expected TypeTime, got %v", tVal.Type)
	}
	if tVal.String() != "14:30:15" {
		t.Errorf("expected 14:30:15, got %s", tVal.String())
	}

	// Comparison
	tEarlier := storage.TimeValueFromString("09:15:00")
	if tVal.Compare(tEarlier) <= 0 {
		t.Errorf("expected 14:30:15 > 09:15:00")
	}
	if tEarlier.Compare(tVal) >= 0 {
		t.Errorf("expected 09:15:00 < 14:30:15")
	}
}
