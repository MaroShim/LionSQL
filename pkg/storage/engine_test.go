package storage

import "testing"

func TestIsTemporaryTable(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "#tmp", want: true},
		{name: "##global_tmp", want: true},
		{name: "__temp_sess1_scratch", want: true},
		{name: "__TEMP_UPPER", want: true},
		{name: "__tv_sess1_cart", want: true},
		{name: "__TV_Items", want: true},
		{name: "__var_sess1_tbl", want: true},
		{name: "__VAR_Rows", want: true},
		{name: "[dbo].[#tmp]", want: true},
		{name: "tempdb.dbo.#tmp", want: true},
		{name: "[__var_local]", want: true},
		{name: "users", want: false},
		{name: "dbo.orders", want: false},
		{name: "_internal", want: false},
		{name: "__sys_catalog", want: false},
		{name: "temp_table", want: false},
		{name: "", want: false},
	}

	for _, tc := range tests {
		if got := IsTemporaryTable(tc.name); got != tc.want {
			t.Errorf("IsTemporaryTable(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
