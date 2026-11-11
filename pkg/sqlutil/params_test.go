package sqlutil

import (
	"fmt"
	"testing"
)

func TestTranslatePgParamsToAtParams(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple single param",
			input:    "SELECT * FROM users WHERE id = $1",
			expected: "SELECT * FROM users WHERE id = @p1",
		},
		{
			name:     "multiple params",
			input:    "SELECT * FROM users WHERE id = $1 AND age > $2 AND active = $3",
			expected: "SELECT * FROM users WHERE id = @p1 AND age > @p2 AND active = @p3",
		},
		{
			name:     "multi-digit param",
			input:    "INSERT INTO items VALUES ($1, $2, $10, $12)",
			expected: "INSERT INTO items VALUES (@p1, @p2, @p10, @p12)",
		},
		{
			name:     "single quotes preserved",
			input:    "SELECT 'Price is $10', id FROM users WHERE id = $1",
			expected: "SELECT 'Price is $10', id FROM users WHERE id = @p1",
		},
		{
			name:     "escaped single quotes preserved",
			input:    "SELECT 'It''s $10 discount' FROM t WHERE id = $1",
			expected: "SELECT 'It''s $10 discount' FROM t WHERE id = @p1",
		},
		{
			name:     "double quotes preserved",
			input:    `SELECT "col$1", "$2" FROM t WHERE id = $1`,
			expected: `SELECT "col$1", "$2" FROM t WHERE id = @p1`,
		},
		{
			name:     "unquoted identifier with dollar preserved",
			input:    `SELECT col$1 FROM t WHERE id = $1`,
			expected: `SELECT col$1 FROM t WHERE id = @p1`,
		},
		{
			name:     "dollar quotes preserved",
			input:    "SELECT $$Hello $1 and $2 world$$ FROM t WHERE id = $1",
			expected: "SELECT $$Hello $1 and $2 world$$ FROM t WHERE id = @p1",
		},
		{
			name:     "tagged dollar quotes preserved",
			input:    "SELECT $tag$body with $1 and $99$tag$ FROM t WHERE id = $1",
			expected: "SELECT $tag$body with $1 and $99$tag$ FROM t WHERE id = @p1",
		},
		{
			name:     "line comments preserved",
			input:    "SELECT * FROM t -- comment with $1 and $2\nWHERE id = $1",
			expected: "SELECT * FROM t -- comment with $1 and $2\nWHERE id = @p1",
		},
		{
			name:     "block comments preserved",
			input:    "SELECT /* block with $1 */ id FROM t WHERE id = $1",
			expected: "SELECT /* block with $1 */ id FROM t WHERE id = @p1",
		},
		{
			name:     "no params unchanged",
			input:    "SELECT 1 FROM users WHERE id = 10",
			expected: "SELECT 1 FROM users WHERE id = 10",
		},
		{
			name:     "dollar sign without digits not matched",
			input:    "SELECT $ FROM t WHERE a = $abc",
			expected: "SELECT $ FROM t WHERE a = $abc",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TranslatePgParamsToAtParams(tc.input)
			if got != tc.expected {
				t.Errorf("\nInput:    %s\nGot:      %s\nExpected: %s", tc.input, got, tc.expected)
			}
		})
	}
}

func TestMaxPgParamIndex(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "no params",
			input:    "SELECT 1",
			expected: 0,
		},
		{
			name:     "single param",
			input:    "SELECT * FROM t WHERE id = $1",
			expected: 1,
		},
		{
			name:     "multiple unordered params",
			input:    "SELECT * FROM t WHERE a = $3 AND b = $1 AND c = $5",
			expected: 5,
		},
		{
			name:     "quoted big numbers ignored",
			input:    "SELECT '$9999' FROM t WHERE id = $1 AND val = $2",
			expected: 2,
		},
		{
			name:     "dollar quotes ignored",
			input:    "SELECT $$ $100 $$ FROM t WHERE id = $3",
			expected: 3,
		},
		{
			name:     "comments ignored",
			input:    "-- $999\nSELECT * FROM t WHERE id = $4",
			expected: 4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MaxPgParamIndex(tc.input)
			if got != tc.expected {
				t.Errorf("\nInput:    %s\nGot:      %d\nExpected: %d", tc.input, got, tc.expected)
			}
		})
	}
}

func TestReplacePgParamsCustom(t *testing.T) {
	sql := "SELECT '$1' FROM t WHERE a = $1 AND b = $2"
	got := ReplacePgParams(sql, func(idx int) string {
		return fmt.Sprintf("param_%d", idx)
	})
	expected := "SELECT '$1' FROM t WHERE a = param_1 AND b = param_2"
	if got != expected {
		t.Errorf("got %q, expected %q", got, expected)
	}
}
