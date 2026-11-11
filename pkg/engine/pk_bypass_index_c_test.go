package engine

import (
	"testing"
)

func TestExtractSimplePKLookup_LiteralWithOperators(t *testing.T) {
	// Query where string literal contains '='
	q1 := "SELECT * FROM users WHERE code = 'A=B'"
	parsed1, ok1 := parsePKLookupQuery(q1)
	if !ok1 || parsed1 == nil {
		t.Fatalf("expected parsePKLookupQuery to succeed for literal with '=', got ok=%v", ok1)
	}
	if parsed1.colName != "code" {
		t.Errorf("expected colName 'code', got %q", parsed1.colName)
	}
	if parsed1.literalVal != "A=B" {
		t.Errorf("expected literalVal 'A=B', got %q", parsed1.literalVal)
	}

	// Query where string literal contains '!='
	q2 := "SELECT * FROM users WHERE note = 'val!=other'"
	parsed2, ok2 := parsePKLookupQuery(q2)
	if !ok2 || parsed2 == nil {
		t.Fatalf("expected parsePKLookupQuery to succeed for literal with '!=', got ok=%v", ok2)
	}
	if parsed2.colName != "note" {
		t.Errorf("expected colName 'note', got %q", parsed2.colName)
	}
	if parsed2.literalVal != "val!=other" {
		t.Errorf("expected literalVal 'val!=other', got %q", parsed2.literalVal)
	}
}

func TestSplitTableAndAlias_TopLevelAS(t *testing.T) {
	tests := []struct {
		input     string
		wantTable string
		wantAlias string
	}{
		{"users AS u", "users", "u"},
		{"[dbo].[users] AS [u]", "[dbo].[users]", "[u]"},
		{"accounts a", "accounts", "a"},
		{"[mydb]..[accounts]", "[mydb]..[accounts]", ""},
	}

	for _, tt := range tests {
		table, alias := splitTableAndAlias(tt.input)
		if table != tt.wantTable || alias != tt.wantAlias {
			t.Errorf("splitTableAndAlias(%q) = (%q, %q); want (%q, %q)", tt.input, table, alias, tt.wantTable, tt.wantAlias)
		}
	}
}
