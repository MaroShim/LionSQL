package tds

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"lionsql/pkg/engine"
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

func TestTypeDateN_Encoding(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "order_date", Type: TypeDateN, Nullable: true},
	}

	// 1. Verify ColMetaData:
	meta := BuildColMetaData(cols)
	if meta[0] != TokenColMetaData {
		t.Fatalf("expected TokenColMetaData 0x81, got 0x%02X", meta[0])
	}
	colCount := binary.LittleEndian.Uint16(meta[1:3])
	if colCount != 1 {
		t.Fatalf("expected colCount 1, got %d", colCount)
	}
	typeByte := meta[9]
	if typeByte != TypeDateN {
		t.Fatalf("expected TypeDateN 0x28, got 0x%02X", typeByte)
	}
	// For TypeDateN, no TYPE_VARLEN follows. Next byte is ColName len
	colNameLen := meta[10]
	if int(colNameLen) != len("order_date") {
		t.Fatalf("expected colNameLen %d, got %d", len("order_date"), colNameLen)
	}

	// 2. Verify ROW binary serialization for 2026-10-15:
	// 2026-10-15 is day 739903 since 0001-01-01.
	targetTime := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	row := BuildRow(cols, []any{targetTime})
	if row[0] != TokenRow {
		t.Fatalf("expected TokenRow 0xD1, got 0x%02X", row[0])
	}
	if row[1] != 3 {
		t.Fatalf("expected date length 3, got %d", row[1])
	}
	days := uint32(row[2]) | (uint32(row[3]) << 8) | (uint32(row[4]) << 16)
	expectedDays := uint32(739903)
	if days != expectedDays {
		t.Fatalf("expected days %d, got %d", expectedDays, days)
	}

	// 3. String input "2026-10-15"
	rowStr := BuildRow(cols, []any{"2026-10-15"})
	daysStr := uint32(rowStr[2]) | (uint32(rowStr[3]) << 8) | (uint32(rowStr[4]) << 16)
	if daysStr != expectedDays {
		t.Fatalf("expected days %d from string, got %d", expectedDays, daysStr)
	}

	// 4. NULL in BuildRow: 1 byte len 0
	rowNull := BuildRow(cols, []any{nil})
	if rowNull[1] != 0 {
		t.Fatalf("expected NULL length 0, got %d", rowNull[1])
	}
}

func TestTypeDateTime2N_Encoding(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "event_ts", Type: TypeDateTime2N, Scale: 7, Nullable: true},
	}

	// 1. Verify ColMetaData:
	meta := BuildColMetaData(cols)
	if meta[9] != TypeDateTime2N {
		t.Fatalf("expected TypeDateTime2N 0x2A, got 0x%02X", meta[9])
	}
	if meta[10] != 7 {
		t.Fatalf("expected scale 7, got %d", meta[10])
	}

	// 2. Verify ROW binary serialization:
	// Target: 2026-10-15 12:34:56.1234567 UTC
	targetTime := time.Date(2026, 10, 15, 12, 34, 56, 123456700, time.UTC)
	row := BuildRow(cols, []any{targetTime})
	if row[0] != TokenRow {
		t.Fatalf("expected TokenRow 0xD1, got 0x%02X", row[0])
	}
	// Length byte for scale 7 is 8 (5 bytes time + 3 bytes date)
	if row[1] != 8 {
		t.Fatalf("expected length 8, got %d", row[1])
	}
	timePart := row[2:7]
	datePart := row[7:10]

	days := uint32(datePart[0]) | (uint32(datePart[1]) << 8) | (uint32(datePart[2]) << 16)
	if days != 739903 {
		t.Fatalf("expected days 739903, got %d", days)
	}

	// Time part: ticks in 100ns increments since midnight
	// 12*3600 + 34*60 + 56 = 45296 seconds = 452,960,000,000 * 100ns + 1234567 = 452,961,234,567 ticks
	ticks := uint64(timePart[0]) | (uint64(timePart[1]) << 8) | (uint64(timePart[2]) << 16) | (uint64(timePart[3]) << 24) | (uint64(timePart[4]) << 32)
	expectedTicks := uint64(452961234567)
	if ticks != expectedTicks {
		t.Fatalf("expected ticks %d, got %d", expectedTicks, ticks)
	}

	// 3. NULL check
	rowNull := BuildRow(cols, []any{nil})
	if rowNull[1] != 0 {
		t.Fatalf("expected NULL length 0, got %d", rowNull[1])
	}
}

func TestTypeGuid_EncodingAndByteSwap(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "id", Type: TypeGuid, Nullable: true},
	}

	// 1. Verify ColMetaData:
	meta := BuildColMetaData(cols)
	if meta[9] != TypeGuid {
		t.Fatalf("expected TypeGuid 0x24, got 0x%02X", meta[9])
	}
	// Max length byte: 16 (0x10)
	if meta[10] != 16 {
		t.Fatalf("expected max length 16, got %d", meta[10])
	}

	// 2. Canonical test GUID:
	// "6F9619FF-8B86-D011-B42D-00C04FC964FF"
	// Part 1: 6F9619FF -> LE FF 19 96 6F
	// Part 2: 8B86     -> LE 86 8B
	// Part 3: D011     -> LE 11 D0
	// Part 4: B42D     -> B4 2D
	// Part 5: 00C04FC964FF -> 00 C0 4F C9 64 FF
	guidStr := "6F9619FF-8B86-D011-B42D-00C04FC964FF"
	row := BuildRow(cols, []any{guidStr})
	if row[0] != TokenRow {
		t.Fatalf("expected TokenRow 0xD1, got 0x%02X", row[0])
	}
	if row[1] != 16 {
		t.Fatalf("expected length 16, got %d", row[1])
	}
	wireBytes := row[2:18]

	expectedHex := "ff19966f868b11d0b42d00c04fc964ff"
	actualHex := hex.EncodeToString(wireBytes)
	if actualHex != expectedHex {
		t.Fatalf("expected wire hex %s, got %s", expectedHex, actualHex)
	}

	// 3. Round-trip decode check:
	decoded := decodeGUID(wireBytes)
	expectedDecoded := "6f9619ff-8b86-d011-b42d-00c04fc964ff"
	if decoded != expectedDecoded {
		t.Fatalf("expected decoded GUID %s, got %s", expectedDecoded, decoded)
	}

	// 4. NULL check:
	rowNull := BuildRow(cols, []any{nil})
	if rowNull[1] != 0 {
		t.Fatalf("expected NULL length 0, got %d", rowNull[1])
	}
}

func TestTypeTimeN_Encoding(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "start_time", Type: TypeTimeN, Scale: 7, Nullable: true},
	}

	// 1. Verify ColMetaData:
	meta := BuildColMetaData(cols)
	if meta[9] != TypeTimeN {
		t.Fatalf("expected TypeTimeN 0x29, got 0x%02X", meta[9])
	}
	if meta[10] != 7 {
		t.Fatalf("expected scale 7, got %d", meta[10])
	}

	// 2. Target time: 14:30:15.1234567
	// 14*3600 + 30*60 + 15 = 52215 seconds
	// 52215 * 10,000,000 + 1234567 = 522,151,234,567 ticks
	targetTime := time.Date(2026, 1, 1, 14, 30, 15, 123456700, time.UTC)
	row := BuildRow(cols, []any{targetTime})
	if row[0] != TokenRow {
		t.Fatalf("expected TokenRow 0xD1, got 0x%02X", row[0])
	}
	if row[1] != 5 {
		t.Fatalf("expected time length 5, got %d", row[1])
	}
	timePart := row[2:7]
	ticks := uint64(timePart[0]) | (uint64(timePart[1]) << 8) | (uint64(timePart[2]) << 16) | (uint64(timePart[3]) << 24) | (uint64(timePart[4]) << 32)
	expectedTicks := uint64(522151234567)
	if ticks != expectedTicks {
		t.Fatalf("expected ticks %d, got %d", expectedTicks, ticks)
	}

	// 3. String input "14:30:15"
	rowStr := BuildRow(cols, []any{"14:30:15"})
	if rowStr[1] != 5 {
		t.Fatalf("expected string time length 5, got %d", rowStr[1])
	}
	ticksStr := uint64(rowStr[2]) | (uint64(rowStr[3]) << 8) | (uint64(rowStr[4]) << 16) | (uint64(rowStr[5]) << 24) | (uint64(rowStr[6]) << 32)
	expectedTicksStr := uint64(522150000000)
	if ticksStr != expectedTicksStr {
		t.Fatalf("expected ticks %d from string, got %d", expectedTicksStr, ticksStr)
	}

	// 4. NULL check:
	rowNull := BuildRow(cols, []any{nil})
	if rowNull[1] != 0 {
		t.Fatalf("expected NULL length 0, got %d", rowNull[1])
	}
}

func TestTypeDateTimeOffsetN_Encoding(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "created_dto", Type: TypeDateTimeOffsetN, Scale: 7, Nullable: true},
	}

	// 1. Verify ColMetaData:
	meta := BuildColMetaData(cols)
	if meta[9] != TypeDateTimeOffsetN {
		t.Fatalf("expected TypeDateTimeOffsetN 0x2B, got 0x%02X", meta[9])
	}
	if meta[10] != 7 {
		t.Fatalf("expected scale 7, got %d", meta[10])
	}

	// 2. Target: 2026-10-15 12:34:56.1234567 +09:00 (KST, +540 minutes)
	kst := time.FixedZone("KST", 9*3600)
	targetTime := time.Date(2026, 10, 15, 12, 34, 56, 123456700, kst)
	row := BuildRow(cols, []any{targetTime})
	if row[0] != TokenRow {
		t.Fatalf("expected TokenRow 0xD1, got 0x%02X", row[0])
	}
	// Total length: 5 (time) + 3 (date) + 2 (offset) = 10 bytes
	if row[1] != 10 {
		t.Fatalf("expected length 10, got %d", row[1])
	}
	timePart := row[2:7]
	datePart := row[7:10]
	offsetPart := row[10:12]

	// In UTC: 12:34:56 - 9 hours = 03:34:56.1234567 UTC on 2026-10-15
	// Date part: 739903
	days := uint32(datePart[0]) | (uint32(datePart[1]) << 8) | (uint32(datePart[2]) << 16)
	if days != 739903 {
		t.Fatalf("expected days 739903, got %d", days)
	}

	// Time in UTC: 3*3600 + 34*60 + 56 = 12896 seconds = 128,961,234,567 ticks
	ticks := uint64(timePart[0]) | (uint64(timePart[1]) << 8) | (uint64(timePart[2]) << 16) | (uint64(timePart[3]) << 24) | (uint64(timePart[4]) << 32)
	expectedTicks := uint64(128961234567)
	if ticks != expectedTicks {
		t.Fatalf("expected UTC ticks %d, got %d", expectedTicks, ticks)
	}

	// Offset: +540 minutes (0x021C LE -> 1C 02)
	offsetMin := int16(binary.LittleEndian.Uint16(offsetPart))
	if offsetMin != 540 {
		t.Fatalf("expected offset 540, got %d", offsetMin)
	}

	// 3. NULL check:
	rowNull := BuildRow(cols, []any{nil})
	if rowNull[1] != 0 {
		t.Fatalf("expected NULL length 0, got %d", rowNull[1])
	}
}

func TestInferColumnInfos_DateDateTime2Guid(t *testing.T) {
	srv := &Server{}
	qr := &engine.QueryResult{
		Columns: []string{"user_id", "birth_date", "created_datetime2", "create_date", "shift_time", "order_offset"},
		Rows: []map[string]any{
			{
				"user_id":           "6F9619FF-8B86-D011-B42D-00C04FC964FF",
				"birth_date":        "1990-05-20",
				"created_datetime2": time.Date(2026, 10, 15, 8, 30, 0, 0, time.UTC),
				"create_date":       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				"shift_time":        "09:30:00",
				"order_offset":      "2026-10-15 12:34:56 +09:00",
			},
		},
	}

	cols := srv.inferColumnInfos(qr)
	if len(cols) != 6 {
		t.Fatalf("expected 6 columns, got %d", len(cols))
	}

	// Column 0: user_id (UUID string) -> TypeGuid (0x24), size 16
	if cols[0].Type != TypeGuid || cols[0].Size != 16 {
		t.Errorf("col 0: expected TypeGuid size 16, got 0x%02X size %d", cols[0].Type, cols[0].Size)
	}

	// Column 1: birth_date (YYYY-MM-DD string) -> TypeDateN (0x28), size 3
	if cols[1].Type != TypeDateN || cols[1].Size != 3 {
		t.Errorf("col 1: expected TypeDateN size 3, got 0x%02X size %d", cols[1].Type, cols[1].Size)
	}

	// Column 2: created_datetime2 -> TypeDateTime2N (0x2A), size 8, scale 7
	if cols[2].Type != TypeDateTime2N || cols[2].Scale != 7 {
		t.Errorf("col 2: expected TypeDateTime2N scale 7, got 0x%02X scale %d", cols[2].Type, cols[2].Scale)
	}

	// Column 3: create_date (sys column) -> TypeDateTimeN (0x6F), size 8
	if cols[3].Type != TypeDateTimeN || cols[3].Size != 8 {
		t.Errorf("col 3: expected TypeDateTimeN size 8, got 0x%02X size %d", cols[3].Type, cols[3].Size)
	}

	// Column 4: shift_time (HH:MM:SS string) -> TypeTimeN (0x29), size 5, scale 7
	if cols[4].Type != TypeTimeN || cols[4].Size != 5 || cols[4].Scale != 7 {
		t.Errorf("col 4: expected TypeTimeN size 5 scale 7, got 0x%02X size %d scale %d", cols[4].Type, cols[4].Size, cols[4].Scale)
	}

	// Column 5: order_offset -> TypeDateTimeOffsetN (0x2B), size 10, scale 7
	if cols[5].Type != TypeDateTimeOffsetN || cols[5].Size != 10 || cols[5].Scale != 7 {
		t.Errorf("col 5: expected TypeDateTimeOffsetN size 10 scale 7, got 0x%02X size %d scale %d", cols[5].Type, cols[5].Size, cols[5].Scale)
	}
}

func TestTypePLP_LOBEncoding(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "doc", Type: TypeVarChar, Size: -1, IsPLP: true, Nullable: true},
	}

	// 1. ColMetaData: MaxLength must be 0xFFFF (PLP indicator)
	meta := BuildColMetaData(cols)
	// ColType is at byte 9 (TypeVarChar 0xA7)
	if meta[9] != TypeVarChar {
		t.Fatalf("expected TypeVarChar 0xA7, got 0x%02X", meta[9])
	}
	// MaxLength is at bytes 10-11: 0xFFFF
	maxLen := binary.LittleEndian.Uint16(meta[10:12])
	if maxLen != 0xFFFF {
		t.Fatalf("expected PLP max length 0xFFFF (65535), got 0x%04X", maxLen)
	}

	// 2. Normal value: payload > 8000 bytes (e.g. 10000 bytes)
	testPayload := strings.Repeat("A", 10000)
	row := BuildRow(cols, []any{testPayload})
	if row[0] != TokenRow {
		t.Fatalf("expected TokenRow 0xD1, got 0x%02X", row[0])
	}
	// Offset 1: 8-byte total length
	totalLen := binary.LittleEndian.Uint64(row[1:9])
	if totalLen != 10000 {
		t.Fatalf("expected total length 10000, got %d", totalLen)
	}
	// Chunk 1 length at offset 9: uint32(8000)
	chunk1Len := binary.LittleEndian.Uint32(row[9:13])
	if chunk1Len != 8000 {
		t.Fatalf("expected chunk 1 length 8000, got %d", chunk1Len)
	}
	// Chunk 2 length at offset 13 + 8000 = 8013: uint32(2000)
	chunk2Len := binary.LittleEndian.Uint32(row[8013:8017])
	if chunk2Len != 2000 {
		t.Fatalf("expected chunk 2 length 2000, got %d", chunk2Len)
	}
	// Terminator chunk at offset 8017 + 2000 = 10017: uint32(0)
	termChunk := binary.LittleEndian.Uint32(row[10017:10021])
	if termChunk != 0 {
		t.Fatalf("expected terminator chunk 0, got %d", termChunk)
	}

	// 3. NULL value: 8-byte 0xFFFFFFFFFFFFFFFF
	nullRow := BuildRow(cols, []any{nil})
	nullLen := binary.LittleEndian.Uint64(nullRow[1:9])
	if nullLen != 0xFFFFFFFFFFFFFFFF {
		t.Fatalf("expected NULL PLP length 0xFFFFFFFFFFFFFFFF, got 0x%016X", nullLen)
	}

	// 4. WriteNBCRowDirect (P-3)
	var buf bytes.Buffer
	rowMap := map[string]any{"doc": testPayload}
	WriteNBCRowDirect(&buf, cols, rowMap)
	nbcBytes := buf.Bytes()
	if nbcBytes[0] != TokenNBCRow {
		t.Fatalf("expected TokenNBCRow 0xD2, got 0x%02X", nbcBytes[0])
	}
	// Null bitmap is 1 byte at index 1: 0 (not null)
	if nbcBytes[1] != 0 {
		t.Fatalf("expected null bitmap 0, got 0x%02X", nbcBytes[1])
	}
	// Offset 2 onwards should be PLP stream
	nbcTotalLen := binary.LittleEndian.Uint64(nbcBytes[2:10])
	if nbcTotalLen != 10000 {
		t.Fatalf("expected NBC total length 10000, got %d", nbcTotalLen)
	}
}

func TestWriteRowDirect_CaseInsensitive(t *testing.T) {
	cols := []ColumnInfo{
		{Name: "UserName", Type: TypeVarChar, Size: 50, Nullable: true},
		{Name: "Age", Type: TypeIntN, Size: 4, Nullable: true},
	}
	// Row map has different casing
	rowMap := map[string]any{
		"username": "alice",
		"AGE":      int64(30),
	}

	var buf bytes.Buffer
	WriteRowDirect(&buf, cols, rowMap)
	res := buf.Bytes()
	if len(res) == 0 || res[0] != TokenRow {
		t.Fatalf("expected TokenRow, got %v", res)
	}

	var nbcBuf bytes.Buffer
	WriteNBCRowDirect(&nbcBuf, cols, rowMap)
	nbcRes := nbcBuf.Bytes()
	if len(nbcRes) == 0 || nbcRes[0] != TokenNBCRow {
		t.Fatalf("expected TokenNBCRow, got %v", nbcRes)
	}
	// Bitmap should have 0 bits set (no NULLs)
	if nbcRes[1] != 0 {
		t.Fatalf("expected 0 NULL bits in bitmap, got 0x%02X", nbcRes[1])
	}
}
