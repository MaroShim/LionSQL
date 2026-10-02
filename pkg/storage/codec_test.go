package storage

import (
	"bytes"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func TestSerialization_ValueAndRecord(t *testing.T) {
	val := IntValue(42)
	b, err := msgpack.Marshal(val)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if len(b) > 5 {
		t.Errorf("expected compact IntValue(42) size <= 5 bytes, got %d bytes", len(b))
	}

	rec := Record{
		"id":   IntValue(1),
		"name": StringValue("Alice"),
		"ts":   DateTimeValue(time.Now()),
	}
	recBytes, err := msgpack.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record failed: %v", err)
	}
	if len(recBytes) > 60 {
		t.Errorf("expected compact record size <= 60 bytes, got %d bytes", len(recBytes))
	}
}

func TestSerialization_AllTypesRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond).UTC()
	testCases := []struct {
		name string
		val  Value
	}{
		{"Int", IntValue(123456789)},
		{"NegativeInt", IntValue(-987654321)},
		{"Float", FloatValue(3.14159265)},
		{"String", StringValue("hello world")},
		{"BoolTrue", BoolValue(true)},
		{"BoolFalse", BoolValue(false)},
		{"DateTime", DateTimeValue(now)},
		{"JSON", JSONValue(`{"key":"value","count":10}`)},
		{"Decimal", DecimalValue("1234567.8901")},
		{"UUID", UUIDValue("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")},
		{"Binary", BinaryValue([]byte{0xDE, 0xAD, 0xBE, 0xEF})},
		{"Date", DateValue(now)},
		{"Time", Value{Type: TypeTime, SVal: "14:30:00"}},
		{"NullInt", NullValue(TypeInt)},
		{"NullVarChar", NullValue(TypeVarChar)},
		{"NullDateTime", NullValue(TypeDateTime)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := msgpack.Marshal(tc.val)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}

			var decoded Value
			if err := msgpack.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			if decoded.IsNull != tc.val.IsNull {
				t.Fatalf("expected IsNull %v, got %v", tc.val.IsNull, decoded.IsNull)
			}
			if decoded.Type != tc.val.Type {
				t.Fatalf("expected Type %v, got %v", tc.val.Type, decoded.Type)
			}
			if !tc.val.IsNull && !decoded.Equal(tc.val) {
				t.Fatalf("expected value %v, got %v", tc.val, decoded)
			}
		})
	}
}

func TestSerialization_LegacyMapBackwardsCompatibility(t *testing.T) {
	// Construct a legacy msgpack map payload representing the old un-optimized Value struct
	legacyMap := map[string]any{
		"Type":   int(TypeInt),
		"IsNull": false,
		"IVal":   int64(9999),
		"SVal":   "",
		"FVal":   float64(9999),
		"BVal":   false,
		"TVal":   time.Time{},
		"DVal":   map[string]any{},
		"BinVal": "",
	}

	buf := new(bytes.Buffer)
	enc := msgpack.NewEncoder(buf)
	if err := enc.Encode(legacyMap); err != nil {
		t.Fatalf("legacy marshal failed: %v", err)
	}

	var decoded Value
	dec := msgpack.NewDecoder(buf)
	if err := dec.Decode(&decoded); err != nil {
		t.Fatalf("legacy unmarshal failed: %v", err)
	}

	if decoded.Type != TypeInt || decoded.IVal != 9999 || decoded.IsNull {
		t.Fatalf("failed to decode legacy value accurately: %+v", decoded)
	}
}

func BenchmarkEncodeIndexKeyPart(b *testing.B) {
	v := IntValue(123456789)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EncodeIndexKeyPart(v)
	}
}
