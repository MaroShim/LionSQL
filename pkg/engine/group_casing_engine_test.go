package engine

import (
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestCleanOutputSuffix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"@timeout", "@timeout"},
		{"@timeout OUTPUT", "@timeout"},
		{"@timeout OUT", "@timeout"},
		{"@shout", "@shout"},
		{"@val OUTPUT", "@val"},
		{"@val OUT", "@val"},
		{"@layout", "@layout"},
		{"@my_var   OUTPUT", "@my_var"},
	}

	for _, tt := range tests {
		got := cleanOutputSuffix(tt.input)
		if got != tt.expected {
			t.Errorf("cleanOutputSuffix(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestStringSplitEmptySeparator(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, DefaultConfig())
	defer coord.Close()

	// 1. STRING_SPLIT with empty separator should fail
	res, err := coord.ExecuteSQL("SELECT * FROM STRING_SPLIT('a,b,c', '');")
	if err == nil && res != nil && res.Success {
		t.Fatalf("expected error for empty separator, got success")
	}
	if err != nil && !strings.Contains(err.Error(), "separator cannot be empty") {
		t.Fatalf("expected error containing 'separator cannot be empty', got: %v", err)
	}

	// 2. STRING_SPLIT with valid separator should succeed
	res, err = coord.ExecuteSQL("SELECT value FROM STRING_SPLIT('foo,bar,baz', ',');")
	if err != nil || !res.Success {
		t.Fatalf("STRING_SPLIT failed: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
}
