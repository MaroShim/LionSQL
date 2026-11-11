package tds

import (
	"bytes"
	"strings"
	"testing"
)

func TestTDS_ZeroAllocRowStreamParity(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "Id", Type: TypeIntN, Size: 4, Nullable: true},
		{Name: "Name", Type: TypeNVarChar, Size: 100, Nullable: true},
		{Name: "Body", Type: TypeNVarChar, Size: -1, IsPLP: true, Nullable: true},
		{Name: "Active", Type: TypeBitN, Size: 1, Nullable: true},
		{Name: "Note", Type: TypeVarChar, Size: 50, Nullable: true},
	}

	longUnicode := strings.Repeat("한글UTF16테스트😃", 600)
	rows := []map[string]any{
		{
			"id":     int64(1),
			"name":   "Alice",
			"body":   longUnicode,
			"active": true,
			"note":   "ok",
		},
		{
			"id":     int64(2),
			"name":   nil,
			"body":   "short",
			"active": nil,
			"note":   "second",
		},
	}
	vals := [][]any{
		{int64(1), "Alice", longUnicode, true, "ok"},
		{int64(2), nil, "short", nil, "second"},
	}

	// Compare WriteRowDirect vs WriteRowDirectValues
	for i := range rows {
		var legacy, fast bytes.Buffer
		WriteRowDirect(&legacy, cols, rows[i])
		WriteRowDirectValues(&fast, cols, vals[i])
		if !bytes.Equal(legacy.Bytes(), fast.Bytes()) {
			t.Fatalf("row %d WriteRowDirect parity mismatch (len %d vs %d)", i, legacy.Len(), fast.Len())
		}

		legacy.Reset()
		fast.Reset()
		WriteNBCRowDirect(&legacy, cols, rows[i])
		WriteNBCRowDirectValues(&fast, cols, vals[i])
		if !bytes.Equal(legacy.Bytes(), fast.Bytes()) {
			t.Fatalf("row %d WriteNBCRowDirect parity mismatch (len %d vs %d)", i, legacy.Len(), fast.Len())
		}
	}

	// Compare PLP UTF-16LE encoding
	for _, s := range []string{"", "ASCII only", longUnicode, "Mixed 한글 and 𠮷野家"} {
		var legacy, fast bytes.Buffer
		writePLPValue(&legacy, EncodeUTF16LE(s))
		writePLPUTF16LE(&fast, s)
		if !bytes.Equal(legacy.Bytes(), fast.Bytes()) {
			t.Fatalf("writePLPUTF16LE mismatch for %q (len %d vs %d)", s, legacy.Len(), fast.Len())
		}
	}

	// Compare tdsStreamWriter framing vs WritePacketWithSize
	var payload bytes.Buffer
	for i := 0; i < 300; i++ {
		WriteRowDirectValues(&payload, cols, vals[i%2])
	}
	var wantWire bytes.Buffer
	if err := WritePacketWithSize(&wantWire, PacketTabular, 1, payload.Bytes(), 4096); err != nil {
		t.Fatalf("WritePacketWithSize failed: %v", err)
	}

	var gotWire bytes.Buffer
	stream := newTdsStreamWriter(&gotWire, PacketTabular, 1, 4096)
	for i := 0; i < 300; i++ {
		WriteRowDirectValues(stream.Buffer(), cols, vals[i%2])
		if err := stream.FlushIfReady(); err != nil {
			t.Fatalf("FlushIfReady failed: %v", err)
		}
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close failed: %v", err)
	}
	if !bytes.Equal(wantWire.Bytes(), gotWire.Bytes()) {
		t.Fatalf("tdsStreamWriter wire framing mismatch: want len %d got len %d", wantWire.Len(), gotWire.Len())
	}
}

type tdsDiscardWriter struct{}

func (tdsDiscardWriter) Write(p []byte) (int, error) { return len(p), nil }

func makeBenchTDSRows(n int) ([]ColumnInfo, []map[string]any, [][]any) {
	cols := []ColumnInfo{
		{Name: "id", Type: TypeIntN, Size: 8, Nullable: false},
		{Name: "balance", Type: TypeFltN, Size: 8, Nullable: false},
		{Name: "name", Type: TypeNVarChar, Size: 100, Nullable: true},
		{Name: "active", Type: TypeBitN, Size: 1, Nullable: true},
	}
	rows := make([]map[string]any, n)
	vals := make([][]any, n)
	for i := 0; i < n; i++ {
		var active any = i%2 == 0
		if i%5 == 0 {
			active = nil
		}
		vals[i] = []any{int64(i + 1), float64(i) * 1.25, "Customer_Record_Payload", active}
		rows[i] = map[string]any{
			"id":      vals[i][0],
			"balance": vals[i][1],
			"name":    vals[i][2],
			"active":  vals[i][3],
		}
	}
	return cols, rows, vals
}

func BenchmarkTDS_LegacyResultSet_100k(b *testing.B) {
	cols, rows, _ := makeBenchTDSRows(100000)
	sess := &ConnectionSession{TDSVersion: 0x74000004, PacketSize: 4096}
	b.ReportAllocs()
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		var buf bytes.Buffer
		scratchVals := make([]any, len(cols))
		for _, row := range rows {
			sess.writeRowDirect(&buf, cols, row, scratchVals)
		}
		_ = WritePacketWithSize(tdsDiscardWriter{}, PacketTabular, 1, buf.Bytes(), sess.PacketSize)
	}
}

func BenchmarkTDS_ZeroAllocStream_100k(b *testing.B) {
	cols, rows, vals := makeBenchTDSRows(100000)
	sess := &ConnectionSession{TDSVersion: 0x74000004, PacketSize: 4096}
	srv := &Server{}
	b.ReportAllocs()
	b.ResetTimer()
	for iter := 0; iter < b.N; iter++ {
		stream := sess.newStreamWriter(tdsDiscardWriter{}, PacketTabular)
		_ = srv.streamQueryRows(stream, sess, cols, rows, vals)
		_ = stream.Close()
	}
}
