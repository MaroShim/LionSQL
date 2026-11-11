package sqlutil

import (
	"reflect"
	"testing"
)

func TestStripParentheses(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		ok       bool
	}{
		{"(a + b)", "a + b", true},
		{"((a + b))", "(a + b)", true},
		{"(a) + (b)", "(a) + (b)", false},
		{"a + b", "a + b", false},
		{"(SELECT 1)", "SELECT 1", true},
		{"  (VALUES (1, 2))  ", "VALUES (1, 2)", true},
		{"(a, '(b, c)')", "a, '(b, c)'", true},
	}

	for _, tc := range tests {
		got, ok := StripParentheses(tc.input)
		if ok != tc.ok || got != tc.expected {
			t.Errorf("StripParentheses(%q) = (%q, %v); want (%q, %v)", tc.input, got, ok, tc.expected, tc.ok)
		}
	}
}

func TestParseInPredicate(t *testing.T) {
	tests := []struct {
		input       string
		wantCol     string
		wantIsNot   bool
		wantListRaw string
		wantItems   []string
		wantOk      bool
	}{
		{
			input:       "id IN (1, 2, 3)",
			wantCol:     "id",
			wantIsNot:   false,
			wantListRaw: "1, 2, 3",
			wantItems:   []string{"1", "2", "3"},
			wantOk:      true,
		},
		{
			input:       "[t].[status] NOT IN ('pending', 'cancelled')",
			wantCol:     "status",
			wantIsNot:   true,
			wantListRaw: "'pending', 'cancelled'",
			wantItems:   []string{"'pending'", "'cancelled'"},
			wantOk:      true,
		},
		{
			input:       "val IN ('a,b', 'c,d')",
			wantCol:     "val",
			wantIsNot:   false,
			wantListRaw: "'a,b', 'c,d'",
			wantItems:   []string{"'a,b'", "'c,d'"},
			wantOk:      true,
		},
		{
			input:       "val = 5",
			wantCol:     "",
			wantIsNot:   false,
			wantListRaw: "",
			wantItems:   nil,
			wantOk:      false,
		},
	}

	for _, tc := range tests {
		pred, ok := ParseInPredicate(tc.input)
		if ok != tc.wantOk {
			t.Errorf("ParseInPredicate(%q) ok = %v; want %v", tc.input, ok, tc.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if pred.Column != tc.wantCol || pred.IsNot != tc.wantIsNot || pred.ListRaw != tc.wantListRaw {
			t.Errorf("ParseInPredicate(%q) = %+v; want col=%s, isNot=%v, listRaw=%s",
				tc.input, pred, tc.wantCol, tc.wantIsNot, tc.wantListRaw)
		}
		if !reflect.DeepEqual(pred.Items, tc.wantItems) {
			t.Errorf("ParseInPredicate(%q).Items = %v; want %v", tc.input, pred.Items, tc.wantItems)
		}
	}
}

func TestParseIsNullPredicate(t *testing.T) {
	tests := []struct {
		input     string
		wantCol   string
		wantIsNot bool
		wantOk    bool
	}{
		{"id IS NULL", "id", false, true},
		{"[t].[name] IS NOT NULL", "name", true, true},
		{"id = 1", "", false, false},
	}

	for _, tc := range tests {
		pred, ok := ParseIsNullPredicate(tc.input)
		if ok != tc.wantOk {
			t.Errorf("ParseIsNullPredicate(%q) ok = %v; want %v", tc.input, ok, tc.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if pred.Column != tc.wantCol || pred.IsNot != tc.wantIsNot {
			t.Errorf("ParseIsNullPredicate(%q) = %+v; want col=%s, isNot=%v", tc.input, pred, tc.wantCol, tc.wantIsNot)
		}
	}
}

func TestParseAnyPredicate(t *testing.T) {
	tests := []struct {
		input     string
		wantCol   string
		wantOp    string
		wantItems []string
		wantOk    bool
	}{
		{"id = ANY (ARRAY[1, 2, 3])", "id", "=", []string{"1", "2", "3"}, true},
		{"name != ANY ('{alice, bob}')", "name", "!=", []string{"alice", "bob"}, true},
		{"id = 1", "", "", nil, false},
	}

	for _, tc := range tests {
		pred, ok := ParseAnyPredicate(tc.input)
		if ok != tc.wantOk {
			t.Errorf("ParseAnyPredicate(%q) ok = %v; want %v", tc.input, ok, tc.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if pred.Column != tc.wantCol || pred.Op != tc.wantOp {
			t.Errorf("ParseAnyPredicate(%q) = %+v; want col=%s, op=%s", tc.input, pred, tc.wantCol, tc.wantOp)
		}
		if !reflect.DeepEqual(pred.Items, tc.wantItems) {
			t.Errorf("ParseAnyPredicate(%q).Items = %v; want %v", tc.input, pred.Items, tc.wantItems)
		}
	}
}

func TestParseBetweenPredicate(t *testing.T) {
	tests := []struct {
		input     string
		wantCol   string
		wantIsNot bool
		wantLow   string
		wantHigh  string
		wantOk    bool
	}{
		{"age BETWEEN 18 AND 65", "age", false, "18", "65", true},
		{"[score] NOT BETWEEN 0 AND 100", "score", true, "0", "100", true},
		{"age = 20", "", false, "", "", false},
	}

	for _, tc := range tests {
		pred, ok := ParseBetweenPredicate(tc.input)
		if ok != tc.wantOk {
			t.Errorf("ParseBetweenPredicate(%q) ok = %v; want %v", tc.input, ok, tc.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if pred.Column != tc.wantCol || pred.IsNot != tc.wantIsNot || pred.Low != tc.wantLow || pred.High != tc.wantHigh {
			t.Errorf("ParseBetweenPredicate(%q) = %+v; want col=%s, isNot=%v, low=%s, high=%s",
				tc.input, pred, tc.wantCol, tc.wantIsNot, tc.wantLow, tc.wantHigh)
		}
	}
}

func TestParseBinaryComparison(t *testing.T) {
	tests := []struct {
		input      string
		wantCol    string
		wantFull   string
		wantOp     string
		wantTarget string
		wantOk     bool
	}{
		{"[c].[name] = 'Orders'", "name", "c.name", "=", "Orders", true},
		{"status != 'ACTIVE'", "status", "status", "!=", "ACTIVE", true},
		{"price >= 100", "price", "price", ">=", "100", true},
		{"title LIKE '%book%'", "title", "title", "LIKE", "%book%", true},
		{"name NOT LIKE 'test%'", "name", "name", "NOT LIKE", "test%", true},
		{"desc ~* 'abc'", "desc", "desc", "~*", "abc", true},
		{"doc->>'key' = 'value'", "key", "doc->>'key'", "=", "value", true},
		{"SELECT 1", "", "", "", "", false},
	}

	for _, tc := range tests {
		comp, ok := ParseBinaryComparison(tc.input)
		if ok != tc.wantOk {
			t.Errorf("ParseBinaryComparison(%q) ok = %v; want %v", tc.input, ok, tc.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if comp.Column != tc.wantCol || comp.FullColumn != tc.wantFull || comp.Op != tc.wantOp || comp.RightClean != tc.wantTarget {
			t.Errorf("ParseBinaryComparison(%q) = %+v; want col=%s, full=%s, op=%s, target=%s",
				tc.input, comp, tc.wantCol, tc.wantFull, tc.wantOp, tc.wantTarget)
		}
	}
}

func TestDesugarBareTableIn(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bare table in",
			input:    "SELECT * FROM t WHERE x IN tbl",
			expected: "SELECT * FROM t WHERE x IN (SELECT * FROM tbl)",
		},
		{
			name:     "NOT IN bare table",
			input:    "SELECT * FROM t WHERE x NOT IN tbl",
			expected: "SELECT * FROM t WHERE x NOT IN (SELECT * FROM tbl)",
		},
		{
			name:     "Already has parens",
			input:    "SELECT * FROM t WHERE x IN (SELECT id FROM tbl)",
			expected: "SELECT * FROM t WHERE x IN (SELECT id FROM tbl)",
		},
		{
			name:     "Literal list",
			input:    "SELECT * FROM t WHERE x IN (1, 2, 3)",
			expected: "SELECT * FROM t WHERE x IN (1, 2, 3)",
		},
		{
			name:     "String literal with IN",
			input:    "SELECT 'IN tbl' FROM t",
			expected: "SELECT 'IN tbl' FROM t",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DesugarBareTableIn(tt.input)
			if got != tt.expected {
				t.Errorf("\nInput:    %s\nExpected: %s\nGot:      %s", tt.input, tt.expected, got)
			}
		})
	}
}

func TestExtractEqualityFilter(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		cols     []string
		expected string
		found    bool
	}{
		{
			name:     "object_id number",
			sql:      "SELECT * FROM sys.columns WHERE object_id = 1005",
			cols:     []string{"object_id"},
			expected: "1005",
			found:    true,
		},
		{
			name:     "qualified table_name",
			sql:      "SELECT * FROM information_schema.tables WHERE t.table_name = 'users'",
			cols:     []string{"table_name", "name"},
			expected: "users",
			found:    true,
		},
		{
			name:     "N prefix unicode literal",
			sql:      "SELECT * FROM sys.tables WHERE [name] = N'Orders'",
			cols:     []string{"name"},
			expected: "Orders",
			found:    true,
		},
		{
			name:     "pg relname",
			sql:      "SELECT * FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relname = 'accounts'",
			cols:     []string{"relname"},
			expected: "accounts",
			found:    true,
		},
		{
			name:     "object_name call in XORM",
			sql:      "SELECT * FROM sys.indexes ixs WHERE OBJECT_NAME(ixs.object_id) = 'users' AND ixs.is_primary_key = 0",
			cols:     []string{"object_name(ixs.object_id)", "object_name"},
			expected: "users",
			found:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ExtractEqualityFilter(tt.sql, tt.cols...)
			if ok != tt.found {
				t.Fatalf("expected found=%v, got=%v", tt.found, ok)
			}
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestExtractObjectIdCall(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		found    bool
	}{
		{
			name:     "object_id with literal",
			input:    "SELECT * FROM sys.columns WHERE object_id = object_id('users')",
			expected: "users",
			found:    true,
		},
		{
			name:     "object_id with N literal and schema",
			input:    "WHERE c.object_id = sys.object_id(N'dbo.orders')",
			expected: "dbo.orders",
			found:    true,
		},
		{
			name:     "object_id with unicode preceding",
			input:    "WHERE x = 'ı' AND object_id('dbo.t') > 0",
			expected: "dbo.t",
			found:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ExtractObjectIdCall(tt.input)
			if ok != tt.found {
				t.Fatalf("expected found=%v, got=%v", tt.found, ok)
			}
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestPredicate_HardeningProbes(t *testing.T) {
	// 1. AnyPredicate operators and Array formatting
	if p, ok := ParseAnyPredicate("a >= ANY(x)"); !ok || p.Op != ">=" || p.Column != "a" {
		t.Fatalf("ParseAnyPredicate >= failed: got %+v, ok=%v", p, ok)
	}
	if p, ok := ParseAnyPredicate("n = ANY(ARRAY['a','b'])"); !ok || len(p.Items) != 2 || p.Items[0] != "a" || p.Items[1] != "b" {
		t.Fatalf("ParseAnyPredicate ARRAY failed: got %+v, ok=%v", p, ok)
	}
	if p, ok := ParseAnyPredicate("n = ANY('{a,b}')"); !ok || len(p.Items) != 2 || p.Items[0] != "a" || p.Items[1] != "b" {
		t.Fatalf("ParseAnyPredicate postgres set failed: got %+v, ok=%v", p, ok)
	}

	// 2. InPredicate subquery rejection
	if _, ok := ParseInPredicate("x IN (SELECT a, b FROM t)"); ok {
		t.Fatalf("expected ParseInPredicate to reject subquery IN, but got ok=true")
	}

	// 3. IsNullPredicate compound and newline tolerance
	if _, ok := ParseIsNullPredicate("a = 1 AND b IS NULL"); ok {
		t.Fatalf("expected ParseIsNullPredicate to reject compound AND condition, but got ok=true")
	}
	if p, ok := ParseIsNullPredicate("b\nIS NULL"); !ok || p.Column != "b" {
		t.Fatalf("ParseIsNullPredicate newline failed: got %+v, ok=%v", p, ok)
	}

	// 4. BinaryComparison comment stripping
	if p, ok := ParseBinaryComparison("a = 1 -- it's comment\n"); !ok || p.RightClean != "1" {
		t.Fatalf("ParseBinaryComparison comment failed: got %+v, ok=%v", p, ok)
	}

	// 5. ExtractEqualityFilter decimal, negative, and nested subquery
	if v, ok := ExtractEqualityFilter("SELECT * FROM t WHERE price = 1.5", "price"); !ok || v != "1.5" {
		t.Fatalf("ExtractEqualityFilter decimal failed: got %q, ok=%v", v, ok)
	}
	if v, ok := ExtractEqualityFilter("SELECT * FROM t WHERE id = -5", "id"); !ok || v != "-5" {
		t.Fatalf("ExtractEqualityFilter negative failed: got %q, ok=%v", v, ok)
	}
	if v, ok := ExtractEqualityFilter("SELECT * FROM t WHERE id IN (SELECT x FROM u WHERE name = 'zz')", "name"); ok {
		t.Fatalf("expected ExtractEqualityFilter to ignore nested subquery equality, but got %q", v)
	}
}
