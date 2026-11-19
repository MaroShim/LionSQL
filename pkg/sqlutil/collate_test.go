package sqlutil

import "testing"

func TestIsCaseSensitiveCollation(t *testing.T) {
	tests := []struct {
		collation string
		want      bool
	}{
		{"Latin1_General_CS_AS", true},
		{"Latin1_General_BIN", true},
		{"Latin1_General_BIN2", true},
		{"SQL_Latin1_General_CP1_CS_AS", true},
		{"Korean_Wansung_CS_AS", true},
		{"Japanese_CS_AS", true},
		{"C", true},
		{"'C'", true},
		{`"POSIX"`, true},
		{"[Latin1_General_CS_AS]", true},
		{"Latin1_General_CI_AS", false},
		{"SQL_Latin1_General_CP1_CI_AS", false},
		{"Korean_Wansung_CI_AS", false},
		{"", false},
		{"default", false},
	}

	for _, tt := range tests {
		got := IsCaseSensitiveCollation(tt.collation)
		if got != tt.want {
			t.Errorf("IsCaseSensitiveCollation(%q) = %v, want %v", tt.collation, got, tt.want)
		}
	}
}
