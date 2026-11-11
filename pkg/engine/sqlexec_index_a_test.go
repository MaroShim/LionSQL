package engine

import (
	"testing"
)

func TestCleanIdentifier_TopLevelWith(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"users WITH (NOLOCK)", "users"},
		{"orders_with_details", "orders_with_details"},
		{"[t_with_flag]", "t_with_flag"},
		{"dbo.products WITH (ROWLOCK)", "products"},
		{"table_with_with_col", "table_with_with_col"},
	}

	for _, tt := range tests {
		got := cleanIdentifier(tt.input)
		if got != tt.want {
			t.Errorf("cleanIdentifier(%q) = %q; want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseAggItem_LiteralContainingAs(t *testing.T) {
	// Query projection where literal contains ' AS '
	proj := "COUNT(CASE WHEN status = ' AS NEW' THEN 1 END) AS count_new"
	item := parseAggItem(proj)

	if item.FuncType != "COUNT" {
		t.Errorf("expected FuncType COUNT, got %q", item.FuncType)
	}
	if item.ArgCol != "CASE WHEN status = ' AS NEW' THEN 1 END" {
		t.Errorf("expected ArgCol without truncation, got %q", item.ArgCol)
	}
}

func TestParseTableAlias_WithHintsAndNames(t *testing.T) {
	tests := []struct {
		input     string
		wantTable string
		wantAlias string
	}{
		{"tbl WITH (HOLDLOCK) AS t", "tbl", "t"},
		{"tbl_with_prefix AS t", "tbl_with_prefix", "t"},
		{"orders WITH(NOLOCK)", "orders", ""},
		{"with_table_name", "with_table_name", ""},
	}

	for _, tt := range tests {
		gotTable, gotAlias := parseTableAlias(tt.input)
		if gotTable != tt.wantTable || gotAlias != tt.wantAlias {
			t.Errorf("parseTableAlias(%q) = (%q, %q); want (%q, %q)", tt.input, gotTable, gotAlias, tt.wantTable, tt.wantAlias)
		}
	}
}
