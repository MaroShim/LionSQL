package sqlutil

import (
	"testing"
)

func TestNormalizeMSSQLPagination(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "SELECT TOP 10",
			input:    "SELECT TOP 10 id, name FROM users",
			expected: "SELECT id, name FROM users LIMIT 10",
		},
		{
			name:     "SELECT TOP with parens",
			input:    "SELECT TOP (5) * FROM products WHERE price > 100",
			expected: "SELECT * FROM products WHERE price > 100 LIMIT 5",
		},
		{
			name:     "SELECT DISTINCT TOP",
			input:    "SELECT DISTINCT TOP 20 category FROM items",
			expected: "SELECT DISTINCT category FROM items LIMIT 20",
		},
		{
			name:     "OFFSET FETCH NEXT",
			input:    "SELECT * FROM users ORDER BY id OFFSET 10 ROWS FETCH NEXT 5 ROWS ONLY",
			expected: "SELECT * FROM users ORDER BY id LIMIT 5 OFFSET 10",
		},
		{
			name:     "OFFSET FETCH FIRST",
			input:    "SELECT * FROM users ORDER BY id OFFSET 20 ROW FETCH FIRST 10 ROW ONLY",
			expected: "SELECT * FROM users ORDER BY id LIMIT 10 OFFSET 20",
		},
		{
			name:     "OFFSET ONLY",
			input:    "SELECT * FROM users ORDER BY id OFFSET 50 ROWS",
			expected: "SELECT * FROM users ORDER BY id OFFSET 50",
		},
		{
			name:     "FETCH ONLY",
			input:    "SELECT * FROM users ORDER BY id FETCH NEXT 15 ROWS ONLY",
			expected: "SELECT * FROM users ORDER BY id LIMIT 15",
		},
		{
			name:     "Strip NOLOCK and OPTION",
			input:    "SELECT TOP 10 * FROM orders WITH (NOLOCK) OPTION (RECOMPILE)",
			expected: "SELECT * FROM orders LIMIT 10",
		},
		{
			name:     "SELECT TOP with trailing semicolon preserved",
			input:    "SELECT TOP 5 a FROM t;",
			expected: "SELECT a FROM t LIMIT 5;",
		},
		{
			name:     "SELECT TOP with PERCENT bypassed",
			input:    "SELECT TOP 10 PERCENT a FROM t",
			expected: "SELECT TOP 10 PERCENT a FROM t",
		},
		{
			name:     "SELECT TOP with WITH TIES bypassed",
			input:    "SELECT TOP 5 WITH TIES a FROM t ORDER BY a",
			expected: "SELECT TOP 5 WITH TIES a FROM t ORDER BY a",
		},
		{
			name:     "SELECT TOP with UNION bypassed",
			input:    "SELECT TOP 5 a FROM t UNION SELECT a FROM u",
			expected: "SELECT TOP 5 a FROM t UNION SELECT a FROM u",
		},
		{
			name:     "NOLOCK inside string literal preserved",
			input:    "SELECT * FROM t WHERE n = 'a with (nolock) b'",
			expected: "SELECT * FROM t WHERE n = 'a with (nolock) b'",
		},
		{
			name:     "NOLOCK with CTE preceding",
			input:    "WITH c AS (SELECT 1 x) SELECT * FROM t WITH (NOLOCK)",
			expected: "WITH c AS (SELECT 1 x) SELECT * FROM t",
		},
		{
			name:     "NOLOCK with unicode characters",
			input:    "SELECT 'ı' c FROM t WITH (NOLOCK)",
			expected: "SELECT 'ı' c FROM t",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeMSSQLPagination(tt.input)
			if got != tt.expected {
				t.Errorf("\nInput:    %s\nExpected: %s\nGot:      %s", tt.input, tt.expected, got)
			}
		})
	}
}
