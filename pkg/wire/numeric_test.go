package wire

import (
	"math/big"
	"testing"

	"lionsql/pkg/storage"
)

func TestPgBinaryNumericRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"zero_int", 0, "0"},
		{"zero_decimal_scale_0", storage.ZeroDecimal(0), "0"},
		{"zero_decimal_scale_2", storage.ZeroDecimal(2), "0.00"},
		{"positive_int", 150, "150"},
		{"negative_int", -42, "-42"},
		{"standard_decimal", "150.00", "150.00"},
		{"negative_decimal", "-150.00", "-150.00"},
		{"fraction_only", "0.50", "0.50"},
		{"small_fraction", "0.05", "0.05"},
		{"very_small_fraction", "0.0005", "0.0005"},
		{"multi_chunk_int", "12345678", "12345678"},
		{"multi_chunk_decimal", "12345678.9012", "12345678.9012"},
		{"float64_input", float64(99.95), "99.95"},
		{"storage_value_decimal", storage.DecimalValueFromDecimal(storage.NewDecimal(big.NewInt(35050), 2)), "350.50"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := encodePgBinaryNumeric(tc.input)
			if len(enc) < 8 {
				t.Fatalf("encoded bytes too short: %d", len(enc))
			}

			dec, err := decodePgBinaryNumeric(enc)
			if err != nil {
				t.Fatalf("decodePgBinaryNumeric failed: %v", err)
			}

			if dec.String() != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, dec.String())
			}
		})
	}
}

func TestPgBinaryNumericMalformed(t *testing.T) {
	// Less than 8 bytes header
	if _, err := decodePgBinaryNumeric([]byte{0, 1, 2}); err == nil {
		t.Errorf("expected error on truncated header, got nil")
	}

	// Stated ndigits = 2 (needs 4 extra bytes), but only 1 byte given
	b := []byte{0, 2, 0, 0, 0, 0, 0, 0, 0}
	if _, err := decodePgBinaryNumeric(b); err == nil {
		t.Errorf("expected error on truncated digits payload, got nil")
	}

	// Negative ndigits (-1 = 0xFFFF)
	negDigits := []byte{0xFF, 0xFF, 0, 0, 0, 0, 0, 0}
	if _, err := decodePgBinaryNumeric(negDigits); err == nil {
		t.Errorf("expected error on negative ndigits, got nil")
	}

	// Out-of-bounds dscale (30000 = 0x7530)
	badScale := []byte{0, 0, 0, 0, 0, 0, 0x75, 0x30}
	if _, err := decodePgBinaryNumeric(badScale); err == nil {
		t.Errorf("expected error on excessive dscale, got nil")
	}

	// Out-of-range base-10000 digit (10000 = 0x2710)
	badDigit := []byte{0, 1, 0, 0, 0, 0, 0, 0, 0x27, 0x10}
	if _, err := decodePgBinaryNumeric(badDigit); err == nil {
		t.Errorf("expected error on digit >= 10000, got nil")
	}

	// Malformed binary array with negative elemLen (-5) or oversized dim
	if got := decodeBinaryArray([]byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 25, 0x7F, 0xFF, 0xFF, 0xFF, 0, 0, 0, 1}, 0); got != "{}" {
		t.Errorf("expected empty array literal on oversized dim, got %q", got)
	}
	if got := decodeBinaryArray([]byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 25, 0, 0, 0, 1, 0, 0, 0, 1, 0xFF, 0xFF, 0xFF, 0xFB}, 0); got != "{}" {
		t.Errorf("expected safe handling on negative elemLen (-5), got %q", got)
	}
}
