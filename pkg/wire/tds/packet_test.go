package tds

import (
	"bytes"
	"strings"
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

// Regression test for P0-27: TDS Packet OOM DoS Guard
func TestTDS_ReadPacket_OOMGuard_P0_27(t *testing.T) {
	t.Run("ExceedSinglePacketFrameSize", func(t *testing.T) {
		// Header with length 40000 (> MaxTDSPacketSize 32768)
		hdr := make([]byte, HeaderSize)
		hdr[0] = PacketSQLBatch
		hdr[1] = StatusEOM
		hdr[2] = byte(40000 >> 8)
		hdr[3] = byte(40000 & 0xFF)

		r := bytes.NewReader(hdr)
		_, err := ReadPacket(r)
		if err == nil {
			t.Fatalf("expected error for packet length exceeding MaxTDSPacketSize, got nil")
		}
		if !strings.Contains(err.Error(), "invalid TDS packet length") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("InvalidLengthSmallerThanHeader", func(t *testing.T) {
		hdr := make([]byte, HeaderSize)
		hdr[0] = PacketSQLBatch
		hdr[1] = StatusEOM
		hdr[2] = 0
		hdr[3] = 4 // < HeaderSize (8)

		r := bytes.NewReader(hdr)
		_, err := ReadPacket(r)
		if err == nil {
			t.Fatalf("expected error for packet length < HeaderSize, got nil")
		}
	})

	t.Run("NormalPacketSuccess", func(t *testing.T) {
		payload := []byte("SELECT 1;")
		pktLen := HeaderSize + len(payload)
		buf := make([]byte, pktLen)
		buf[0] = PacketSQLBatch
		buf[1] = StatusEOM
		buf[2] = byte(pktLen >> 8)
		buf[3] = byte(pktLen & 0xFF)
		copy(buf[HeaderSize:], payload)

		r := bytes.NewReader(buf)
		pkt, err := ReadPacket(r)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(pkt.Payload) != "SELECT 1;" {
			t.Fatalf("expected payload %q, got %q", "SELECT 1;", string(pkt.Payload))
		}
	})
}

// Regression test for AUDIT-FINAL-SAFETY-NET-1: TDS RPC/PLP & VarChar Bounds/OOM Guards
func TestTDS_MalformedRPCAndPLP_BoundsGuard(t *testing.T) {
	t.Run("ReadBVarChar_TruncatedPayload", func(t *testing.T) {
		// Claims 50 UTF-16LE chars (100 bytes), but only 4 bytes follow
		r := bytes.NewReader([]byte{50, 0x41, 0x00, 0x42, 0x00})
		if _, err := ReadBVarChar(r); err == nil {
			t.Fatalf("expected error on truncated ReadBVarChar")
		}
	})

	t.Run("ReadUsVarChar_TruncatedPayload", func(t *testing.T) {
		// Claims 60000 UTF-16LE chars (120000 bytes), but only 2 bytes follow
		r := bytes.NewReader([]byte{0x60, 0xEA, 0x41, 0x00})
		if _, err := ReadUsVarChar(r); err == nil {
			t.Fatalf("expected error on truncated ReadUsVarChar")
		}
	})

	t.Run("ParseRPCRequest_TruncatedProcName", func(t *testing.T) {
		// nameLen = 10000 (0x2710), but no name bytes follow
		payload := []byte{0x10, 0x27}
		if _, err := parseRPCRequest(payload); err == nil {
			t.Fatalf("expected error on truncated RPC proc name")
		}
	})

	t.Run("ReadRPCValue_PLPOversizedChunkLen", func(t *testing.T) {
		// TypeVarChar (0xA7), maxLen=0xFFFF (PLP), totalLen=0 (unknown), chunkLen=0x7FFFFFFF (2GB) with only 2 bytes in buffer
		buf := []byte{
			0xFF, 0xFF, // maxLen = PLP
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // collation (5 bytes) + 1
		}
		// Build exact TypeVarChar PLP stream: maxLen(2) + collation(5) + totalLen(8) + chunkLen(4)
		stream := []byte{
			0xFF, 0xFF, // maxLen = 0xFFFF (PLP)
			0x09, 0x04, 0xD0, 0x00, 0x34, // collation (5 bytes)
			0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // totalLen = PLP_UNKNOWN
			0xFF, 0xFF, 0xFF, 0x7F, // chunkLen = 2GB (malicious!)
			0x41, 0x42, // only 2 bytes present
		}
		_ = buf
		r := bytes.NewReader(stream)
		if _, err := readRPCValue(r, TypeVarChar); err == nil {
			t.Fatalf("expected error on oversized PLP chunkLen, got nil")
		}
	})

	t.Run("ReadRPCValue_DecimalNInvalidScaleAndLength", func(t *testing.T) {
		// TypeDecimalN: maxLen=17, prec=38, scale=200 (invalid > 38), actualLen=5
		stream := []byte{17, 38, 200, 5, 1, 1, 0, 0, 0}
		r := bytes.NewReader(stream)
		if _, err := readRPCValue(r, TypeDecimalN); err == nil {
			t.Fatalf("expected error on invalid DecimalN scale > 38")
		}
	})
}
