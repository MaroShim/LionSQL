package execution_test

import (
	"testing"
	"time"

	"sqlion/pkg/execution"
)

func TestGovernor_EstimateTupleSize(t *testing.T) {
	tuple := execution.Tuple{
		"id":        int64(1001),
		"name":      "Alice in Wonderland",
		"price":     99.95,
		"active":    true,
		"createdAt": time.Now(),
	}

	size := execution.EstimateTupleSize(tuple)
	if size <= 0 {
		t.Fatalf("expected positive tuple size, got %d", size)
	}
	if size < 100 || size > 1000 {
		t.Errorf("tuple size outside reasonable bounds: %d", size)
	}
}

func TestGovernor_ParseAndFormatMemSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		format   string
	}{
		{"64MB", 64 * 1024 * 1024, "64MB"},
		{"'128MB'", 128 * 1024 * 1024, "128MB"},
		{"1GB", 1024 * 1024 * 1024, "1GB"},
		{"16kB", 16 * 1024, "16kB"},
		{"4096", 4096, "4kB"},
		{"4096B", 4096, "4kB"},
	}

	for _, tc := range tests {
		bytes, err := execution.ParseMemSize(tc.input)
		if err != nil {
			t.Fatalf("ParseMemSize(%q) failed: %v", tc.input, err)
		}
		if bytes != tc.expected {
			t.Errorf("ParseMemSize(%q) = %d, expected %d", tc.input, bytes, tc.expected)
		}
		formatted := execution.FormatMemSize(bytes)
		if formatted != tc.format {
			t.Errorf("FormatMemSize(%d) = %q, expected %q", bytes, formatted, tc.format)
		}
	}
}
