package storage_test

import (
	"math"
	"testing"

	"lionsql/pkg/storage"
)

func TestRealVsFloatTupleCodecV2(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "test_rf",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val_real", Type: storage.TypeReal},
			{Name: "val_float", Type: storage.TypeFloat},
		},
	}

	valR := float32(3.141592)
	valF := float64(3.141592653589793)

	row := storage.Row{
		Values: []storage.Value{
			storage.IntValue(1),
			storage.RealValue(valR),
			storage.FloatValue(valF),
		},
	}

	encoded, err := storage.EncodeRow(schema, row)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	// Null bitmap length for 3 cols: ceil(3/8) = 1 byte
	// Header: Version(1B) + Flag(1B) + Cols(2B) + Bitmap(1B) = 5 bytes
	// IntValue(1): varint (1 byte)
	// RealValue: 4 bytes
	// FloatValue: 8 bytes
	// Total payload: 5 + 1 + 4 + 8 = 18 bytes
	expectedLen := 5 + 1 + 4 + 8
	if len(encoded) != expectedLen {
		t.Fatalf("expected encoded length %d (4B real + 8B float), got %d", expectedLen, len(encoded))
	}

	decoded, err := storage.DecodeTupleToRow(schema, encoded)
	if err != nil {
		t.Fatalf("DecodeTupleToRow failed: %v", err)
	}

	if decoded.Values[1].Type != storage.TypeReal {
		t.Fatalf("expected val_real type TypeReal, got %v", decoded.Values[1].Type)
	}
	if decoded.Values[2].Type != storage.TypeFloat {
		t.Fatalf("expected val_float type TypeFloat, got %v", decoded.Values[2].Type)
	}

	gotR := float32(decoded.Values[1].FVal)
	if math.Abs(float64(gotR-valR)) > 1e-6 {
		t.Fatalf("expected val_real %v, got %v", valR, gotR)
	}
	if ifaceR, ok := decoded.Values[1].Interface().(float32); !ok || ifaceR != gotR {
		t.Fatalf("expected Value.Interface() to return float32, got %T (%v)", decoded.Values[1].Interface(), decoded.Values[1].Interface())
	}

	gotF := decoded.Values[2].FVal
	if gotF != valF {
		t.Fatalf("expected val_float %v, got %v", valF, gotF)
	}
	if ifaceF, ok := decoded.Values[2].Interface().(float64); !ok || ifaceF != gotF {
		t.Fatalf("expected Value.Interface() to return float64, got %T (%v)", decoded.Values[2].Interface(), decoded.Values[2].Interface())
	}
}

func TestRealVsFloatIndexKeyEncoding(t *testing.T) {
	// REAL index key must be 8 hex characters (4 bytes sign-flipped)
	r1 := storage.RealValue(-123.45)
	r2 := storage.RealValue(0.0)
	r3 := storage.RealValue(123.45)

	kR1 := storage.EncodeIndexKeyPart(r1)
	kR2 := storage.EncodeIndexKeyPart(r2)
	kR3 := storage.EncodeIndexKeyPart(r3)

	if len(kR1) != 8 || len(kR2) != 8 || len(kR3) != 8 {
		t.Fatalf("expected 8 hex characters for TypeReal, got len=%d, %d, %d", len(kR1), len(kR2), len(kR3))
	}
	if !(kR1 < kR2 && kR2 < kR3) {
		t.Fatalf("expected monotonic order for TypeReal: %s < %s < %s failed", kR1, kR2, kR3)
	}

	// FLOAT index key must be 16 hex characters (8 bytes sign-flipped)
	f1 := storage.FloatValue(-123.45)
	f2 := storage.FloatValue(0.0)
	f3 := storage.FloatValue(123.45)

	kF1 := storage.EncodeIndexKeyPart(f1)
	kF2 := storage.EncodeIndexKeyPart(f2)
	kF3 := storage.EncodeIndexKeyPart(f3)

	if len(kF1) != 16 || len(kF2) != 16 || len(kF3) != 16 {
		t.Fatalf("expected 16 hex characters for TypeFloat, got len=%d, %d, %d", len(kF1), len(kF2), len(kF3))
	}
	if !(kF1 < kF2 && kF2 < kF3) {
		t.Fatalf("expected monotonic order for TypeFloat: %s < %s < %s failed", kF1, kF2, kF3)
	}
}
