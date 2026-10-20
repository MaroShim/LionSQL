package sqlutil

import (
	"reflect"
	"testing"
)

func TestSplitTopLevel(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		delimiter rune
		expected  []string
	}{
		{
			name:      "simple comma split",
			input:     "a, b, c",
			delimiter: ',',
			expected:  []string{"a", "b", "c"},
		},
		{
			name:      "comma inside single quotes with escaped quote",
			input:     "1, 'Doe, John', 'O''Reilly, Inc.', 4",
			delimiter: ',',
			expected:  []string{"1", "'Doe, John'", "'O''Reilly, Inc.'", "4"},
		},
		{
			name:      "comma inside parentheses",
			input:     "COALESCE(col1, 'def'), DATEADD(day, 1, col2), 100",
			delimiter: ',',
			expected:  []string{"COALESCE(col1, 'def')", "DATEADD(day, 1, col2)", "100"},
		},
		{
			name:      "comma inside brackets and double quotes",
			input:     `[col, 1], "quoted, col", col3`,
			delimiter: ',',
			expected:  []string{`[col, 1]`, `"quoted, col"`, "col3"},
		},
		{
			name:      "comma inside comments",
			input:     "a, /* comment, with comma */ b, -- line, comment\n c",
			delimiter: ',',
			expected:  []string{"a", "/* comment, with comma */ b", "-- line, comment\n c"},
		},
		{
			name:      "nested parentheses and quotes",
			input:     "(1, 'a,b', (2, 3)), 4",
			delimiter: ',',
			expected:  []string{"(1, 'a,b', (2, 3))", "4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitTopLevel(tt.input, tt.delimiter)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("expected %#v, got %#v", tt.expected, got)
			}
		})
	}
}

func TestParseOrderBy(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []OrderByItem
	}{
		{
			name:     "single asc column",
			input:    "id ASC",
			expected: []OrderByItem{{Column: "id", Desc: false}},
		},
		{
			name:     "multiple columns with desc and asc",
			input:    "col1 DESC, col2 ASC, col3",
			expected: []OrderByItem{
				{Column: "col1", Desc: true},
				{Column: "col2", Desc: false},
				{Column: "col3", Desc: false},
			},
		},
		{
			name:     "order by with comma inside function",
			input:    "COALESCE(score, 0) DESC, [created_at] ASC",
			expected: []OrderByItem{
				{Column: "COALESCE(score, 0)", Desc: true},
				{Column: "[created_at]", Desc: false},
			},
		},
		{
			name:     "empty clause",
			input:    "   ",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseOrderBy(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("expected %#v, got %#v", tt.expected, got)
			}
		})
	}
}

func TestSplitAndConditions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple and split",
			input:    "a = 1 AND b = 2 AND c = 3",
			expected: []string{"a = 1", "b = 2", "c = 3"},
		},
		{
			name:     "and inside quotes and brackets",
			input:    "name = 'O''Reilly AND Sons' AND [col AND other] = 1 AND (x = 1 AND y = 2)",
			expected: []string{
				"name = 'O''Reilly AND Sons'",
				"[col AND other] = 1",
				"(x = 1 AND y = 2)",
			},
		},
		{
			name:     "word boundaries check",
			input:    "standard = 1 AND brand = 'Band' AND x = 10",
			expected: []string{"standard = 1", "brand = 'Band'", "x = 10"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitAndConditions(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("expected %#v, got %#v", tt.expected, got)
			}
		})
	}
}

func TestSplitSetClauses(t *testing.T) {
	input := "name = 'Doe, John', age = 30, score = COALESCE(src.score, 0)"
	expected := []SetPair{
		{Column: "name", Value: "'Doe, John'"},
		{Column: "age", Value: "30"},
		{Column: "score", Value: "COALESCE(src.score, 0)"},
	}

	got := SplitSetClauses(input)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %#v, got %#v", expected, got)
	}
}

func TestSplitAsAlias(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedExpr  string
		expectedAlias string
		expectedHasAs bool
	}{
		{
			name:          "simple as alias",
			input:         "col AS my_col",
			expectedExpr:  "col",
			expectedAlias: "my_col",
			expectedHasAs: true,
		},
		{
			name:          "cast with as inside parenthesis",
			input:         "CAST(col AS VARCHAR(50)) AS my_str",
			expectedExpr:  "CAST(col AS VARCHAR(50))",
			expectedAlias: "my_str",
			expectedHasAs: true,
		},
		{
			name:          "cast without alias",
			input:         "CAST(col AS INT)",
			expectedExpr:  "CAST(col AS INT)",
			expectedAlias: "",
			expectedHasAs: false,
		},
		{
			name:          "identifier without alias",
			input:         "[user_id]",
			expectedExpr:  "[user_id]",
			expectedAlias: "",
			expectedHasAs: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, alias, hasAs := SplitAsAlias(tt.input)
			if expr != tt.expectedExpr || alias != tt.expectedAlias || hasAs != tt.expectedHasAs {
				t.Fatalf("for %q expected (%q, %q, %v), got (%q, %q, %v)",
					tt.input, tt.expectedExpr, tt.expectedAlias, tt.expectedHasAs, expr, alias, hasAs)
			}
		})
	}
}

func TestFindTopLevelKeyword(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		keyword  string
		expected int
	}{
		{
			name:     "from inside substring function",
			input:    "SELECT SUBSTRING(name FROM 1 FOR 3) FROM users",
			keyword:  "FROM",
			expected: 36, // The second FROM outside parenthesis
		},
		{
			name:     "from in column name from_date",
			input:    "SELECT from_date FROM logs",
			keyword:  "FROM",
			expected: 17,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindTopLevelKeyword(tt.input, tt.keyword)
			if got != tt.expected {
				t.Fatalf("expected index %d, got %d", tt.expected, got)
			}
		})
	}
}

func TestSplitJoinCond(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedLeft  string
		expectedRight string
	}{
		{
			name:          "simple equality",
			input:         "a.id = b.id",
			expectedLeft:  "a.id",
			expectedRight: "b.id",
		},
		{
			name:          "parenthesized equality",
			input:         "(a.id = b.id)",
			expectedLeft:  "a.id",
			expectedRight: "b.id",
		},
		{
			name:          "equals inside string literal",
			input:         "a.key = 'x=1'",
			expectedLeft:  "a.key",
			expectedRight: "'x=1'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left, right := SplitJoinCond(tt.input)
			if left != tt.expectedLeft || right != tt.expectedRight {
				t.Fatalf("expected (%q, %q), got (%q, %q)", tt.expectedLeft, tt.expectedRight, left, right)
			}
		})
	}
}
