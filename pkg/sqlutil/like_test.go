package sqlutil

import (
	"testing"
)

func TestMatchLike(t *testing.T) {
	tests := []struct {
		pattern         string
		str             string
		caseInsensitive bool
		expected        bool
	}{
		{"", "", false, true},
		{"", "a", false, false},
		{"%", "", false, true},
		{"%%", "", false, true},
		{"%", "hello world", false, true},
		{"_", "", false, false},
		{"_", "a", false, true},
		{"_", "ab", false, false},
		{"a%", "apple", false, true},
		{"a%", "banana", false, false},
		{"%e", "apple", false, true},
		{"%e", "banana", false, false},
		{"%pp%", "apple", false, true},
		{"%px%", "apple", false, false},
		{"a_p_e", "apple", false, true},
		{"a_p_e", "aple", false, false},
		{"a%e", "ae", false, true},
		{"a%e", "aXYZXe", false, true},
		{"Admin_%", "Admin_1", false, true},
		{"Admin_%", "Admin", false, false},
		{"Admin_%", "admin_1", false, false},
		{"Admin_%", "admin_1", true, true},
		{"%world", "Hello WORLD", true, true},
		{"%WORLD", "hello world", false, false},
		{"가%다", "가나다", false, true},
		{"가_다", "가나다", false, true},
		{"가_다", "가나다라", false, false},
		{"%[_]CS[_]AS%", "Latin1_General_CS_AS", false, true},
		{"%[_]CS[_]AS%", "Latin1_General_CI_AS", false, false},
		{"%[%]%", "discount 50% off", false, true},
		{"%[%]%", "discount 50 off", false, false},
		{"[A-Z]%", "Apple", false, true},
		{"[A-Z]%", "apple", false, false},
		{"[A-Z]%", "apple", true, true},
		{"[^0-9]%", "abc", false, true},
		{"[^0-9]%", "123", false, false},
		{"100%", "100% Cotton", true, true},
	}

	for _, tc := range tests {
		got := MatchLike(tc.pattern, tc.str, tc.caseInsensitive)
		if got != tc.expected {
			t.Errorf("MatchLike(%q, %q, %v) = %v; want %v", tc.pattern, tc.str, tc.caseInsensitive, got, tc.expected)
		}
	}
}
