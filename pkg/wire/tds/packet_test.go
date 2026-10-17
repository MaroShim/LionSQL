package tds

import (
	"bytes"
	"testing"
)

func TestUTF16LE_ASCII(t *testing.T) {
	orig := "SELECT * FROM Users WHERE id = 1 AND name = 'Alice';"
	encoded := EncodeUTF16LE(orig)
	if len(encoded) != len(orig)*2 {
		t.Fatalf("expected encoded length %d, got %d", len(orig)*2, len(encoded))
	}
	decoded := DecodeUTF16LE(encoded)
	if decoded != orig {
		t.Fatalf("expected %q, got %q", orig, decoded)
	}
}

func TestUTF16LE_Unicode(t *testing.T) {
	orig := "안녕하세요 세계! 世界你好 123"
	encoded := EncodeUTF16LE(orig)
	decoded := DecodeUTF16LE(encoded)
	if decoded != orig {
		t.Fatalf("expected %q, got %q", orig, decoded)
	}
}

func TestUTF16LE_SurrogatePairs(t *testing.T) {
	orig := "Rocket 🚀 Star 🌟 Fire 🔥 Math 𝐀𝐁𝐂"
	encoded := EncodeUTF16LE(orig)
	decoded := DecodeUTF16LE(encoded)
	if decoded != orig {
		t.Fatalf("expected %q, got %q", orig, decoded)
	}
}

func TestUTF16LE_EdgeCases(t *testing.T) {
	// Empty string
	if len(EncodeUTF16LE("")) != 0 {
		t.Fatalf("expected empty slice for empty string")
	}
	if DecodeUTF16LE(nil) != "" || DecodeUTF16LE([]byte{}) != "" {
		t.Fatalf("expected empty string for nil/empty bytes")
	}

	// Odd number of bytes (should truncate last odd byte safely)
	b := []byte{'H', 0x00, 'i', 0x00, 0xFF}
	if got := DecodeUTF16LE(b); got != "Hi" {
		t.Fatalf("expected 'Hi', got %q", got)
	}
}

func BenchmarkEncodeUTF16LE_ASCII(b *testing.B) {
	s := "SELECT id, name, email, department FROM employees WHERE status = 'ACTIVE' ORDER BY id ASC;"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = EncodeUTF16LE(s)
	}
}

func BenchmarkEncodeUTF16LE_Unicode(b *testing.B) {
	s := "SELECT * FROM 부서목록 WHERE 부서명 = '플랫폼개발팀' AND 활성화여부 = 1;"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = EncodeUTF16LE(s)
	}
}

func BenchmarkDecodeUTF16LE_ASCII(b *testing.B) {
	s := "SELECT id, name, email, department FROM employees WHERE status = 'ACTIVE' ORDER BY id ASC;"
	raw := EncodeUTF16LE(s)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = DecodeUTF16LE(raw)
	}
}

func BenchmarkDecodeUTF16LE_Unicode(b *testing.B) {
	s := "SELECT * FROM 부서목록 WHERE 부서명 = '플랫폼개발팀' AND 활성화여부 = 1;"
	raw := EncodeUTF16LE(s)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = DecodeUTF16LE(raw)
	}
}

func BenchmarkTDS_WriteRowDirect(b *testing.B) {
	cols := []ColumnInfo{
		{Name: "id", Type: TypeIntN, Size: 4},
		{Name: "name", Type: TypeVarChar, Size: 50},
		{Name: "balance", Type: TypeFltN, Size: 8},
	}
	row := map[string]any{
		"id":      12345,
		"name":    "Acme Corporation",
		"balance": 98765.43,
	}

	var buf bytes.Buffer
	buf.Grow(1024)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		WriteRowDirect(&buf, cols, row)
	}
}

func BenchmarkTDS_BuildRow(b *testing.B) {
	cols := []ColumnInfo{
		{Name: "id", Type: TypeIntN, Size: 4},
		{Name: "name", Type: TypeVarChar, Size: 50},
		{Name: "balance", Type: TypeFltN, Size: 8},
	}
	vals := []any{
		12345,
		"Acme Corporation",
		98765.43,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = BuildRow(cols, vals)
	}
}
