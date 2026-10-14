package tds

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBuildNBCRow_SingleColumnNull(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "col1", Type: TypeIntN, Size: 4, Nullable: true},
	}
	values := []any{nil}

	raw := BuildNBCRow(cols, values)
	if len(raw) == 0 {
		t.Fatalf("BuildNBCRow returned empty byte slice")
	}

	// 1 byte TokenNBCRow (0xD2)
	if raw[0] != TokenNBCRow {
		t.Fatalf("expected TokenNBCRow 0xD2, got 0x%02X", raw[0])
	}

	// 1 byte bitmap: bit 0 = 1 (NULL), length = ceil(1/8) = 1
	if len(raw) != 2 {
		t.Fatalf("expected 2 bytes total (token + 1 byte bitmap), got %d", len(raw))
	}
	if raw[1] != 0x01 {
		t.Fatalf("expected bitmap byte 0x01, got 0x%02X", raw[1])
	}
}

func TestBuildNBCRow_MixedColumns(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "id", Type: TypeIntN, Size: 4, Nullable: false},
		{Name: "name", Type: TypeVarChar, Size: 50, Nullable: true},
		{Name: "val", Type: TypeFltN, Size: 8, Nullable: false},
		{Name: "note", Type: TypeNVarChar, Size: 100, Nullable: true},
	}
	// id=42 (not null), name=nil (null), val=3.14 (not null), note=nil (null)
	values := []any{42, nil, 3.14, nil}

	raw := BuildNBCRow(cols, values)
	if raw[0] != TokenNBCRow {
		t.Fatalf("expected TokenNBCRow 0xD2, got 0x%02X", raw[0])
	}

	// Bitmap length: ceil(4/8) = 1 byte
	// Bit 0: id -> 0
	// Bit 1: name -> 1
	// Bit 2: val -> 0
	// Bit 3: note -> 1
	// Expected bitmap: 0b00001010 = 0x0A
	bitmap := raw[1]
	if bitmap != 0x0A {
		t.Fatalf("expected bitmap 0x0A, got 0x%02X", bitmap)
	}

	// Payload starts at offset 2
	payload := raw[2:]

	// Verify col 0 (id=42, TypeInt4): 1 byte len = 4, followed by 42 in int32 LE
	if len(payload) < 5 {
		t.Fatalf("payload too short for int4: len %d", len(payload))
	}
	if payload[0] != 4 {
		t.Fatalf("expected int length 4, got %d", payload[0])
	}
	idVal := binary.LittleEndian.Uint32(payload[1:5])
	if idVal != 42 {
		t.Fatalf("expected id 42, got %d", idVal)
	}

	// Col 1 is omitted (0 bytes)

	// Col 2 (val=3.14, TypeFltN): 1 byte len = 8, followed by float64 LE
	fltPayload := payload[5:]
	if len(fltPayload) != 9 {
		t.Fatalf("expected 9 bytes for flt8 (1 len + 8 data), got %d", len(fltPayload))
	}
	if fltPayload[0] != 8 {
		t.Fatalf("expected flt length 8, got %d", fltPayload[0])
	}

	// Col 3 is omitted (0 bytes)
}

func TestBuildNBCRow_MultiByteBitmap(t *testing.T) {
	// 10 columns: ceil(10/8) = 2 bytes bitmap
	cols := make([]ColumnInfo, 10)
	for i := range cols {
		cols[i] = ColumnInfo{Name: "c", Type: TypeIntN, Size: 4, Nullable: true}
	}
	// Column 0 and Column 9 are NULL, rest are non-NULL
	values := make([]any, 10)
	for i := range values {
		values[i] = i * 10
	}
	values[0] = nil
	values[9] = nil

	raw := BuildNBCRow(cols, values)
	if raw[0] != TokenNBCRow {
		t.Fatalf("expected TokenNBCRow 0xD2, got 0x%02X", raw[0])
	}

	// Byte 0: column 0 is NULL -> bit 0 = 1 -> 0x01
	// Byte 1: column 9 is NULL -> bit (9%8)=1 = 1 -> 0x02
	if raw[1] != 0x01 {
		t.Fatalf("expected bitmap byte 0 to be 0x01, got 0x%02X", raw[1])
	}
	if raw[2] != 0x02 {
		t.Fatalf("expected bitmap byte 1 to be 0x02, got 0x%02X", raw[2])
	}

	// Exactly 8 non-NULL columns written (each 1 len byte + 4 value bytes = 5 bytes -> 40 bytes)
	dataPayload := raw[3:]
	if len(dataPayload) != 8*5 {
		t.Fatalf("expected 40 bytes of data, got %d", len(dataPayload))
	}
}

func TestBuildNBCRow_AllNotNull(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "a", Type: TypeIntN, Size: 4, Nullable: true},
		{Name: "b", Type: TypeVarChar, Size: 50, Nullable: true},
	}
	values := []any{100, "hello"}

	raw := BuildNBCRow(cols, values)
	if raw[0] != TokenNBCRow {
		t.Fatalf("expected TokenNBCRow 0xD2, got 0x%02X", raw[0])
	}
	if raw[1] != 0x00 {
		t.Fatalf("expected bitmap 0x00 for all non-null, got 0x%02X", raw[1])
	}
	if !bytes.Contains(raw, []byte("hello")) {
		t.Fatalf("expected payload to contain 'hello'")
	}
}
