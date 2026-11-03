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
