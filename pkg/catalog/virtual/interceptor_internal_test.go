package virtual

import "testing"

func TestContainsFold(t *testing.T) {
	tests := []struct {
		s      string
		substr string
		want   bool
	}{
		{"SELECT * FROM sys.columns", "sys.", true},
		{"SELECT * FROM SYS.COLUMNS", "sys.", true},
		{"SELECT * FROM Sys.Columns", "sys.", true},
		{"select * from pg_class", "pg_", true},
		{"SELECT * FROM PG_CLASS", "pg_", true},
		{"SELECT * FROM information_schema.tables", "information_schema.", true},
		{"SELECT * FROM INFORMATION_SCHEMA.TABLES", "information_schema.", true},
		{"SELECT * FROM users WHERE id = 1", "sys.", false},
		{"SELECT * FROM users WHERE id = 1", "pg_", false},
		{"SELECT * FROM users WHERE id = 1", "information_schema.", false},
		{"", "sys.", false},
		{"sys", "sys.", false},
		{"sys.", "sys.", true},
	}

	for _, tt := range tests {
		got := containsFold(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("containsFold(%q, %q) = %v; want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestContainsEq_WhitespacePermutations(t *testing.T) {
	cases := []struct {
		name string
		s    string
		left string
		right string
		want bool
	}{
		{"both sides single space", "SELECT * FROM SYS.OBJECTS WHERE O.TYPE = 'TR'", "O.TYPE", "'TR'", true},
		{"no spaces", "SELECT * FROM SYS.OBJECTS WHERE O.TYPE='TR'", "O.TYPE", "'TR'", true},
		{"left space only", "SELECT * FROM SYS.OBJECTS WHERE O.TYPE ='TR'", "O.TYPE", "'TR'", true},
		{"right space only", "SELECT * FROM SYS.OBJECTS WHERE O.TYPE= 'TR'", "O.TYPE", "'TR'", true},
		{"multiple spaces", "SELECT * FROM SYS.OBJECTS WHERE O.TYPE   =   'TR'", "O.TYPE", "'TR'", true},
		{"tabs and newlines", "SELECT * FROM SYS.OBJECTS WHERE O.TYPE\t=\n'TR'", "O.TYPE", "'TR'", true},
		{"is primary key variations 1", "SELECT * FROM SYS.INDEXES WHERE IS_PRIMARY_KEY=1", "IS_PRIMARY_KEY", "1", true},
		{"is primary key variations 2", "SELECT * FROM SYS.INDEXES WHERE IS_PRIMARY_KEY =1", "IS_PRIMARY_KEY", "1", true},
		{"is primary key variations 3", "SELECT * FROM SYS.INDEXES WHERE IS_PRIMARY_KEY= 1", "IS_PRIMARY_KEY", "1", true},
		{"is primary key variations 4", "SELECT * FROM SYS.INDEXES WHERE IS_PRIMARY_KEY = 1", "IS_PRIMARY_KEY", "1", true},
		{"contype variations", "WHERE CONTYPE='F'", "CONTYPE", "'F'", true},
		{"contype variations space", "WHERE CONTYPE = 'F'", "CONTYPE", "'F'", true},
		{"type V variations", "WHERE TYPE='V'", "TYPE", "'V'", true},
		{"type V right space only", "WHERE TYPE= 'V'", "TYPE", "'V'", true},
		{"mismatched right value", "WHERE O.TYPE = 'U'", "O.TYPE", "'TR'", false},
		{"word boundary guard - prefix", "WHERE SOME_TYPE = 'TR'", "TYPE", "'TR'", false},
		{"multiple occurrences second matches", "WHERE O.TYPE = 'U' AND O.TYPE = 'TR'", "O.TYPE", "'TR'", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := containsEq(tc.s, tc.left, tc.right)
			if got != tc.want {
				t.Errorf("containsEq(%q, %q, %q) = %v; want %v", tc.s, tc.left, tc.right, got, tc.want)
			}
		})
	}
}
