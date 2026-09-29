package proc_test

import (
	"strings"
	"testing"

	"sqlion/pkg/proc"
)

func parseExprStr(t *testing.T, s string) proc.Expr {
	t.Helper()
	p := proc.NewParser(proc.NewLexer(s))
	expr := p.ParseExpr()
	if len(p.Errors()) > 0 {
		t.Fatalf("failed to parse expression %q: %v", s, p.Errors())
	}
	return expr
}

func TestFoldConstants_Arithmetic(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"3000 * 12", "36000"},
		{"100 + 50", "150"},
		{"200 - 75", "125"},
		{"100 / 4", "25"},
		{"salary > 3000 * 12", "salary > 36000"},
		{"(10 + 20) * 3", "90"},
		{"total = (50 * 2) + 5", "total = 105"},
	}

	for _, tt := range tests {
		expr := parseExprStr(t, tt.input)
		folded := proc.FoldConstants(expr)
		if folded == nil {
			t.Fatalf("folded expression is nil for %q", tt.input)
		}
		got := strings.TrimSpace(folded.String())
		if got != tt.expected {
			t.Errorf("FoldConstants(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestFoldConstants_LogicalBooleanSimplifications(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1 = 1", "true"},
		{"1 = 0", "false"},
		{"active = 1 AND 1 = 1", "active = 1"},
		{"1 = 1 AND active = 1", "active = 1"},
		{"active = 1 AND 1 = 0", "false"},
		{"1 = 0 AND active = 1", "false"},
		{"active = 1 OR 1 = 0", "active = 1"},
		{"1 = 0 OR active = 1", "active = 1"},
		{"active = 1 OR 1 = 1", "true"},
		{"1 = 1 OR active = 1", "true"},
		{"NOT (1 = 0)", "true"},
		{"NOT (1 = 1)", "false"},
		{"NOT (NOT (active = 1))", "active = 1"},
		{"status = 'A' AND (10 > 5)", "status = 'A'"},
	}

	for _, tt := range tests {
		expr := parseExprStr(t, tt.input)
		folded := proc.FoldConstants(expr)
		if folded == nil {
			t.Fatalf("folded expression is nil for %q", tt.input)
		}
		got := strings.TrimSpace(folded.String())
		if got != tt.expected {
			t.Errorf("FoldConstants(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestFoldConstants_DeterministicFunctions(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"UPPER('hello')", "'HELLO'"},
		{"LOWER('WORLD')", "'world'"},
		{"ABS(-42)", "42"},
		{"LEN('abcdef')", "6"},
		{"CONCAT('foo', 'bar')", "'foobar'"},
		{"status = UPPER('pending')", "status = 'PENDING'"},
	}

	for _, tt := range tests {
		expr := parseExprStr(t, tt.input)
		folded := proc.FoldConstants(expr)
		if folded == nil {
			t.Fatalf("folded expression is nil for %q", tt.input)
		}
		got := strings.TrimSpace(folded.String())
		if got != tt.expected {
			t.Errorf("FoldConstants(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestFoldConstants_NonDeterministicPreserved(t *testing.T) {
	// Functions like GETDATE(), NOW(), RAND() must NEVER be folded at compile time
	nonDetExprs := []string{
		"created_at >= GETDATE()",
		"created_at >= NOW()",
	}

	for _, input := range nonDetExprs {
		expr := parseExprStr(t, input)
		folded := proc.FoldConstants(expr)
		if folded == nil {
			t.Fatalf("folded expression is nil for %q", input)
		}
		got := strings.TrimSpace(folded.String())
		// Ensure GETDATE/NOW is preserved in the string
		if !strings.Contains(strings.ToUpper(got), "GETDATE") && !strings.Contains(strings.ToUpper(got), "NOW") {
			t.Errorf("non-deterministic function was prematurely folded: %q -> %q", input, got)
		}
	}
}

func TestFoldConstants_ComparisonsAndIsNull(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"'test' = 'test'", "true"},
		{"'abc' != 'xyz'", "true"},
		{"100 < 50", "false"},
		{"'val' IS NULL", "false"},
		{"'val' IS NOT NULL", "true"},
	}

	for _, tt := range tests {
		expr := parseExprStr(t, tt.input)
		folded := proc.FoldConstants(expr)
		if folded == nil {
			t.Fatalf("folded expression is nil for %q", tt.input)
		}
		got := strings.TrimSpace(folded.String())
		if got != tt.expected {
			t.Errorf("FoldConstants(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestFoldConstants_UnchangedColumnExpressions(t *testing.T) {
	// Column references should remain unchanged
	exprs := []string{
		"salary > 50000",
		"age BETWEEN 20 AND 30",
		"dept_id IN (1, 2, 3)",
	}

	for _, input := range exprs {
		expr := parseExprStr(t, input)
		folded := proc.FoldConstants(expr)
		if folded == nil {
			t.Fatalf("folded expression is nil for %q", input)
		}
		got := strings.TrimSpace(folded.String())
		if got != input {
			t.Errorf("expected %q to remain unchanged, got %q", input, got)
		}
	}
}
