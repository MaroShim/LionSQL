package wire

import (
	"bytes"
	"testing"
	"time"

	"lionsql/pkg/storage"
)

func TestPGWire_ZeroAllocRowEncodeParity(t *testing.T) {
	colNames := []string{"id", "score", "name", "active", "ts", "dec", "bin", "empty"}
	colOIDs := []int32{20, 701, 25, 16, 1114, 1700, 17, 25}
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)

	rows := []map[string]any{
		{
			"id":     storage.IntValue(42),
			"score":  storage.FloatValue(3.14159),
			"name":   storage.StringValue("LionSQL"),
			"active": storage.BoolValue(true),
			"ts":     storage.DateTimeValue(now),
			"dec":    storage.DecimalValue("123.45"),
			"bin":    storage.BinaryValue([]byte{0xDE, 0xAD, 0xBE, 0xEF}),
			"empty":  nil,
		},
		{
			"id":     int64(-9001),
			"score":  float64(99.5),
			"name":   "raw-string",
			"active": false,
			"ts":     now,
			"dec":    int32(77),
			"bin":    []byte{0x01, 0x02},
			"empty":  nil,
		},
	}

	rowVals := [][]any{
		{rows[0]["id"], rows[0]["score"], rows[0]["name"], rows[0]["active"], rows[0]["ts"], rows[0]["dec"], rows[0]["bin"], rows[0]["empty"]},
		{rows[1]["id"], rows[1]["score"], rows[1]["name"], rows[1]["active"], rows[1]["ts"], rows[1]["dec"], rows[1]["bin"], rows[1]["empty"]},
	}

	for _, formats := range [][]int16{nil, {0}, {1}, {1, 1, 0, 1, 0, 0, 1, 0}} {
		var legacyBuf bytes.Buffer
		scratch := make([][]byte, len(colNames))
		for rIdx, row := range rows {
			for i, colName := range colNames {
				isBinary := (len(formats) == 1 && formats[0] == 1) || (i < len(formats) && formats[i] == 1)
				scratch[i] = formatColumnValue(rowVals[rIdx][i], colOIDs[i], isBinary)
				_ = row[colName]
			}
			if err := WriteDataRowBytes(&legacyBuf, scratch); err != nil {
				t.Fatalf("legacy WriteDataRowBytes failed: %v", err)
			}
		}

		var fastBuf bytes.Buffer
		if err := writePgDataRows(&fastBuf, colNames, colOIDs, formats, rows, rowVals, false); err != nil {
			t.Fatalf("writePgDataRows failed: %v", err)
		}

		if !bytes.Equal(legacyBuf.Bytes(), fastBuf.Bytes()) {
			t.Fatalf("formats=%v parity mismatch:\nlegacy=%x\nfast  =%x", formats, legacyBuf.Bytes(), fastBuf.Bytes())
		}
	}

	// Verify typed NULLs encode as SQL NULL (-1) in both text and binary modes.
	nullRow := [][]any{{storage.NullValue(storage.TypeVarChar), storage.NullValue(storage.TypeInt)}}
	for _, fmts := range [][]int16{{0, 0}, {1, 1}} {
		var buf bytes.Buffer
		if err := writePgDataRows(&buf, []string{"a", "b"}, []int32{25, 23}, fmts, nil, nullRow, false); err != nil {
			t.Fatalf("writePgDataRows null failed: %v", err)
		}
		want := []byte{'D', 0, 0, 0, 14, 0, 2, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
		if !bytes.Equal(buf.Bytes(), want) {
			t.Fatalf("fmts=%v typed NULL mismatch: got %x want %x", fmts, buf.Bytes(), want)
		}
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func makeBenchPgRows(n int) ([]string, []int32, []map[string]any, [][]any) {
	colNames := []string{"id", "balance", "name", "active"}
	colOIDs := []int32{20, 701, 25, 16}
	rows := make([]map[string]any, n)
	vals := make([][]any, n)
	for i := 0; i < n; i++ {
		v0 := storage.IntValue(int64(i + 1))
		v1 := storage.FloatValue(float64(i) * 1.25)
		v2 := storage.StringValue("Customer_Record_Payload")
		v3 := storage.BoolValue(i%2 == 0)
		vals[i] = []any{v0, v1, v2, v3}
		rows[i] = map[string]any{
			"id":      v0,
			"balance": v1,
			"name":    v2,
			"active":  v3,
		}
	}
	return colNames, colOIDs, rows, vals
}

func BenchmarkPGWire_LegacyDataRows_100k(b *testing.B) {
	colNames, colOIDs, rows, vals := makeBenchPgRows(100000)
	scratch := make([][]byte, len(colNames))
	b.ReportAllocs()
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		for rIdx := range rows {
			rv := vals[rIdx]
			for i := range colNames {
				scratch[i] = formatColumnValue(rv[i], colOIDs[i], false)
			}
			_ = WriteDataRowBytes(discardWriter{}, scratch)
		}
	}
}

func BenchmarkPGWire_ZeroAllocStream_100k(b *testing.B) {
	colNames, colOIDs, rows, vals := makeBenchPgRows(100000)
	b.ReportAllocs()
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		if err := writePgDataRows(discardWriter{}, colNames, colOIDs, nil, rows, vals, false); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPGWire_ZeroAllocStream_Binary_100k(b *testing.B) {
	colNames, colOIDs, rows, vals := makeBenchPgRows(100000)
	formats := []int16{1}
	b.ReportAllocs()
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		if err := writePgDataRows(discardWriter{}, colNames, colOIDs, formats, rows, vals, false); err != nil {
			b.Fatal(err)
		}
	}
}
