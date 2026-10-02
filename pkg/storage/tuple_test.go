package storage

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func TestTupleCodec_RoundTripAndNulls(t *testing.T) {
	schema := &TableSchema{
		Name: "users",
		Columns: []Column{
			{Name: "id", Type: TypeInt, PrimaryKey: true},
			{Name: "name", Type: TypeVarChar},
			{Name: "balance", Type: TypeFloat},
			{Name: "is_active", Type: TypeBool},
			{Name: "created_at", Type: TypeDateTime},
			{Name: "metadata", Type: TypeJSON},
			{Name: "salary", Type: TypeDecimal},
			{Name: "uuid", Type: TypeUUID},
			{Name: "avatar", Type: TypeBinary},
			{Name: "extra_null", Type: TypeVarChar, Nullable: true},
		},
	}

	now := time.Now().Truncate(time.Millisecond).UTC()
	rec := Record{
		"id":         IntValue(1001),
		"name":       StringValue("Alice Cooper"),
		"balance":    FloatValue(1234.56),
		"is_active":  BoolValue(true),
		"created_at": DateTimeValue(now),
		"metadata":   JSONValue(`{"role":"admin"}`),
		"salary":     DecimalValue("98765.43"),
		"uuid":       UUIDValue("550e8400-e29b-41d4-a716-446655440000"),
		"avatar":     BinaryValue([]byte{0xCA, 0xFE, 0xBA, 0xBE}),
		"extra_null": NullValue(TypeVarChar),
	}

	data, err := EncodeTuple(schema, rec)
	if err != nil {
		t.Fatalf("EncodeTuple failed: %v", err)
	}

	mpkData, _ := msgpack.Marshal(rec)
	jsonData, _ := json.Marshal(rec)

	t.Logf("Sizes -> Binary Tuple: %d bytes | MsgPack: %d bytes | JSON: %d bytes", len(data), len(mpkData), len(jsonData))

	if len(data) >= len(mpkData) {
		t.Errorf("expected binary tuple to be strictly smaller than msgpack: %d vs %d", len(data), len(mpkData))
	}

	decoded, err := DecodeTuple(schema, data)
	if err != nil {
		t.Fatalf("DecodeTuple failed: %v", err)
	}

	for _, col := range schema.Columns {
		orig := rec[col.Name]
		dec := decoded[col.Name]

		if orig.IsNull != dec.IsNull {
			t.Fatalf("column %s: isNull mismatch: %v vs %v", col.Name, orig.IsNull, dec.IsNull)
		}
		if !orig.IsNull && !orig.Equal(dec) {
			t.Fatalf("column %s: value mismatch: %v vs %v", col.Name, orig, dec)
		}
	}
}

func TestRecordAndRowConversion(t *testing.T) {
	schema := &TableSchema{
		Columns: []Column{
			{Name: "id", Type: TypeInt},
			{Name: "name", Type: TypeVarChar},
			{Name: "active", Type: TypeBool},
		},
	}
	rec := Record{
		"id":     IntValue(100),
		"name":   StringValue("Bob"),
		"active": BoolValue(true),
	}

	row := schema.RecordToRow(rec)
	if len(row.Values) != 3 {
		t.Fatalf("expected 3 row values, got %d", len(row.Values))
	}
	if row.Values[0].IVal != 100 || row.Values[1].SVal != "Bob" || !row.Values[2].BVal {
		t.Fatalf("row value mismatch: %+v", row.Values)
	}

	reconstructed := schema.RowToRecord(row)
	if reconstructed["id"].IVal != 100 || reconstructed["name"].SVal != "Bob" || !reconstructed["active"].BVal {
		t.Fatalf("reconstructed record mismatch: %+v", reconstructed)
	}
}

func BenchmarkTupleCodec_Encode(b *testing.B) {
	schema := &TableSchema{
		Columns: []Column{
			{Name: "id", Type: TypeInt},
			{Name: "name", Type: TypeVarChar},
			{Name: "balance", Type: TypeFloat},
			{Name: "created_at", Type: TypeDateTime},
		},
	}
	rec := Record{
		"id":         IntValue(1),
		"name":       StringValue("Alice"),
		"balance":    FloatValue(100.5),
		"created_at": DateTimeValue(time.Now()),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = EncodeTuple(schema, rec)
	}
}

func BenchmarkTupleCodec_Decode(b *testing.B) {
	schema := &TableSchema{
		Columns: []Column{
			{Name: "id", Type: TypeInt},
			{Name: "name", Type: TypeVarChar},
			{Name: "balance", Type: TypeFloat},
			{Name: "created_at", Type: TypeDateTime},
		},
	}
	rec := Record{
		"id":         IntValue(1),
		"name":       StringValue("Alice"),
		"balance":    FloatValue(100.5),
		"created_at": DateTimeValue(time.Now()),
	}
	data, _ := EncodeTuple(schema, rec)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DecodeTuple(schema, data)
	}
}

func TestEncodeRow_RoundTrip(t *testing.T) {
	schema := &TableSchema{
		Columns: []Column{
			{Name: "id", Type: TypeInt},
			{Name: "name", Type: TypeVarChar},
			{Name: "balance", Type: TypeFloat},
		},
	}
	row := Row{
		Values: []Value{
			IntValue(42),
			StringValue("Zero-Copy Row"),
			FloatValue(99.9),
		},
	}

	data, err := EncodeRow(schema, row)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	decodedRow, err := DecodeTupleToRow(schema, data)
	if err != nil {
		t.Fatalf("DecodeTupleToRow failed: %v", err)
	}

	if len(decodedRow.Values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(decodedRow.Values))
	}
	if decodedRow.Values[0].IVal != 42 || decodedRow.Values[1].SVal != "Zero-Copy Row" || decodedRow.Values[2].FVal != 99.9 {
		t.Fatalf("decoded row mismatch: %+v", decodedRow.Values)
	}
}

func BenchmarkTupleCodec_EncodeRow(b *testing.B) {
	schema := &TableSchema{
		Columns: []Column{
			{Name: "id", Type: TypeInt},
			{Name: "name", Type: TypeVarChar},
			{Name: "balance", Type: TypeFloat},
			{Name: "created_at", Type: TypeDateTime},
		},
	}
	row := Row{
		Values: []Value{
			IntValue(1),
			StringValue("Alice"),
			FloatValue(100.5),
			DateTimeValue(time.Now()),
		},
	}

	buf := make([]byte, 0, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = EncodeRowToBuffer(schema, row, buf)
	}
}

