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

func TestLazyTuple_SelectiveAndRandomAccess(t *testing.T) {
	schema := &TableSchema{
		Name: "test_lazy",
		Columns: []Column{
			{Name: "c0_int", Type: TypeInt},
			{Name: "c1_varchar", Type: TypeVarChar},
			{Name: "c2_float", Type: TypeFloat},
			{Name: "c3_bool", Type: TypeBool},
			{Name: "c4_json", Type: TypeJSON},
			{Name: "c5_null", Type: TypeVarChar, Nullable: true},
			{Name: "c6_dec", Type: TypeDecimal},
		},
	}

	row := Row{
		Values: []Value{
			IntValue(999),
			StringValue("Large text content that should be skipped during non-matching where predicate"),
			FloatValue(3.14159),
			BoolValue(true),
			JSONValue(`{"key":"heavy_payload","arr":[1,2,3,4,5]}`),
			NullValue(TypeVarChar),
			DecimalValue("12345.678"),
		},
	}

	data, err := EncodeRow(schema, row)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	lazy, err := NewLazyTuple(schema, data)
	if err != nil {
		t.Fatalf("NewLazyTuple failed: %v", err)
	}

	// 1. None should be decoded yet
	for i := 0; i < len(schema.Columns); i++ {
		if lazy.IsDecoded(i) {
			t.Fatalf("col %d should not be decoded yet", i)
		}
	}

	// 2. Access c3_bool (index 3) first (simulating WHERE c3_bool = true)
	v3, err := lazy.Get(3)
	if err != nil {
		t.Fatalf("lazy.Get(3) failed: %v", err)
	}
	if !v3.BVal {
		t.Fatalf("expected true, got %v", v3.BVal)
	}
	if !lazy.IsDecoded(3) {
		t.Fatalf("c3 should be decoded")
	}
	// c4_json, c5_null, c6_dec should still NOT be decoded!
	if lazy.IsDecoded(4) || lazy.IsDecoded(6) {
		t.Fatalf("c4 and c6 should not be decoded yet")
	}

	// 3. Now access c0_int (index 0) - testing out-of-order access for already skipped columns
	v0, err := lazy.Get(0)
	if err != nil {
		t.Fatalf("lazy.Get(0) failed: %v", err)
	}
	if v0.IVal != 999 {
		t.Fatalf("expected 999, got %d", v0.IVal)
	}

	// 4. Access null column c5_null (index 5)
	v5, err := lazy.Get(5)
	if err != nil {
		t.Fatalf("lazy.Get(5) failed: %v", err)
	}
	if !v5.IsNull {
		t.Fatalf("expected c5 to be null")
	}

	// 5. Materialize remaining
	fullRow, err := lazy.MaterializeRow()
	if err != nil {
		t.Fatalf("MaterializeRow failed: %v", err)
	}
	if len(fullRow.Values) != len(schema.Columns) {
		t.Fatalf("expected %d columns, got %d", len(schema.Columns), len(fullRow.Values))
	}
	if fullRow.Values[1].SVal != row.Values[1].SVal {
		t.Fatalf("c1 mismatch: %s vs %s", fullRow.Values[1].SVal, row.Values[1].SVal)
	}
	if fullRow.Values[4].SVal != row.Values[4].SVal {
		t.Fatalf("c4 mismatch: %s vs %s", fullRow.Values[4].SVal, row.Values[4].SVal)
	}
	if fullRow.Values[6].Decimal().String() != row.Values[6].Decimal().String() {
		t.Fatalf("c6 mismatch: %s vs %s", fullRow.Values[6].Decimal().String(), row.Values[6].Decimal().String())
	}
}

func TestDecodeTupleSelected(t *testing.T) {
	schema := &TableSchema{
		Name: "test_sel",
		Columns: []Column{
			{Name: "id", Type: TypeInt},
			{Name: "huge_text", Type: TypeVarChar},
			{Name: "status", Type: TypeInt},
			{Name: "huge_json", Type: TypeJSON},
			{Name: "amount", Type: TypeFloat},
		},
	}

	row := Row{
		Values: []Value{
			IntValue(10),
			StringValue("huge text payload that we want to avoid decoding"),
			IntValue(1),
			JSONValue(`{"heavy":true,"data":[1,2,3]}`),
			FloatValue(456.78),
		},
	}

	data, err := EncodeRow(schema, row)
	if err != nil {
		t.Fatalf("EncodeRow failed: %v", err)
	}

	// Select only id (0) and status (2)
	selRow, err := DecodeTupleSelected(schema, data, []int{0, 2})
	if err != nil {
		t.Fatalf("DecodeTupleSelected failed: %v", err)
	}

	if selRow.Values[0].IVal != 10 {
		t.Fatalf("expected id=10, got %d", selRow.Values[0].IVal)
	}
	if selRow.Values[2].IVal != 1 {
		t.Fatalf("expected status=1, got %d", selRow.Values[2].IVal)
	}
	// Columns 1 and 3 should be null / unpopulated
	if !selRow.Values[1].IsNull || !selRow.Values[3].IsNull {
		t.Fatalf("unselected columns should be marked null")
	}
}

func BenchmarkLazyTuple_SelectiveVsFullDecode(b *testing.B) {
	schema := &TableSchema{
		Columns: []Column{
			{Name: "id", Type: TypeInt},
			{Name: "name", Type: TypeVarChar},
			{Name: "bio", Type: TypeVarChar},
			{Name: "details", Type: TypeJSON},
			{Name: "status", Type: TypeInt},
		},
	}
	row := Row{
		Values: []Value{
			IntValue(1),
			StringValue("Alice Cooper"),
			StringValue("A long biography description with many sentences explaining history and background."),
			JSONValue(`{"profile":{"theme":"dark","settings":{"notifications":true,"nested":{"val":42}}}}`),
			IntValue(100),
		},
	}
	data, _ := EncodeRow(schema, row)

	b.Run("FullDecode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r, _ := DecodeTupleToRow(schema, data)
			_ = r.Values[4].IVal
		}
	})

	b.Run("LazySelectiveDecode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			lt, _ := NewLazyTuple(schema, data)
			v, _ := lt.Get(4) // only evaluate status
			_ = v.IVal
		}
	})
}

