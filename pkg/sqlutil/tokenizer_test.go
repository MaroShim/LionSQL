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

func TestSplitTopLevelKeyword(t *testing.T) {
	// Keyword in literal or word boundary
	s := "name = 'O''Reilly AND Sons' AND status = 1 AND band = 'Rock'"
	parts := SplitTopLevelKeyword(s, "AND")
	expected := []string{"name = 'O''Reilly AND Sons'", "status = 1", "band = 'Rock'"}
	if !reflect.DeepEqual(parts, expected) {
		t.Fatalf("expected %#v, got %#v", expected, parts)
	}

	// OR splitting
	s2 := "col1 = 1 OR (col2 = 'OR' AND col3 = 2) OR col4 = 3"
	parts2 := SplitTopLevelKeyword(s2, "OR")
	expected2 := []string{"col1 = 1", "(col2 = 'OR' AND col3 = 2)", "col4 = 3"}
	if !reflect.DeepEqual(parts2, expected2) {
		t.Fatalf("expected %#v, got %#v", expected2, parts2)
	}
}

func TestFindTopLevelKeyword_Advanced(t *testing.T) {
	s := "SELECT id, 'FROM' AS f FROM tbl WHERE name = 'FROM'"
	idx := FindTopLevelKeyword(s, "FROM")
	if idx != 23 { // index of the real 'FROM'
		t.Fatalf("expected index 23, got %d", idx)
	}

	s2 := "INSERT INTO t VALUES (1) ON CONFLICT (id) DO NOTHING"
	idx2 := FindTopLevelKeyword(s2, "ON CONFLICT")
	if idx2 != 25 {
		t.Fatalf("expected index 25, got %d", idx2)
	}
}

func TestFindTopLevelArithmeticOp(t *testing.T) {
	// Precedence check: lowest binding (+, -) before (*, /)
	idx, op := FindTopLevelArithmeticOp("10 + 20 * 30")
	if idx != 3 || op != '+' {
		t.Fatalf("expected op '+' at idx 3, got %c at idx %d", op, idx)
	}

	// Inside string literal
	idx2, op2 := FindTopLevelArithmeticOp("'10 + 20' * 30")
	if idx2 != 10 || op2 != '*' {
		t.Fatalf("expected op '*' at idx 10, got %c at idx %d", op2, idx2)
	}
}

func TestExtractTopLevelTuples(t *testing.T) {
	s := "(1, 'Hello, World'), (2, 'Tuple with (nested) paren'), (3, 'O''Reilly')"
	tuples := ExtractTopLevelTuples(s)
	expected := []string{
		"1, 'Hello, World'",
		"2, 'Tuple with (nested) paren'",
		"3, 'O''Reilly'",
	}
	if !reflect.DeepEqual(tuples, expected) {
		t.Fatalf("expected %#v, got %#v", expected, tuples)
	}
}

func TestFindAllTopLevelKeyword(t *testing.T) {
	s := "WHEN MATCHED THEN UPDATE SET a = 1 WHEN NOT MATCHED THEN INSERT (a) VALUES (2)"
	indices := FindAllTopLevelKeyword(s, "WHEN")
	if len(indices) != 2 || indices[0] != 0 || indices[1] != 35 {
		t.Fatalf("expected [0, 35], got %v", indices)
	}

	// Inside string literal and comments
	s2 := "SELECT 'WHEN NOT' /* WHEN */ WHEN MATCHED"
	indices2 := FindAllTopLevelKeyword(s2, "WHEN")
	if len(indices2) != 1 || indices2[0] != 29 {
		t.Fatalf("expected [29], got %v", indices2)
	}
}

func TestFindTopLevelChar(t *testing.T) {
	s := "col = 'val = 1' AND b = 2"
	idx := FindTopLevelChar(s, '=')
	if idx != 4 {
		t.Fatalf("expected idx 4, got %d", idx)
	}

	// Inside parenthesis
	s2 := "(a = 1) = true"
	idx2 := FindTopLevelChar(s2, '=')
	if idx2 != 8 {
		t.Fatalf("expected idx 8, got %d", idx2)
	}
}



func TestContainsKeyword(t *testing.T) {
	tests := []struct {
		name               string
		str                string
		keyword            string
		allowInsideParens  bool
		want               bool
	}{
		{"Top-level keyword found", "SELECT a FROM t WHERE b = 1", "FROM", false, true},
		{"Keyword inside parens not matched when allow=false", "SELECT a FROM (SELECT 1 AS b) t", "AS", false, false},
		{"Keyword inside parens matched when allow=true", "SELECT a FROM (SELECT 1 AS b) t", "AS", true, true},
		{"Disjunction OR with spaces", "a = 1 OR b = 2", "OR", true, true},
		{"Disjunction OR with tabs", "a = 1\tOR\tb = 2", "OR", true, true},
		{"Disjunction OR with newlines", "a = 1\nOR\nb = 2", "OR", true, true},
		{"Disjunction OR inside parens", "(a = 1 OR b = 2)", "OR", true, true},
		{"OR inside single quotes ignored", "col = 'FOO OR BAR'", "OR", true, false},
		{"OR inside double quotes ignored", `"MY OR COL" = 1`, "OR", true, false},
		{"OR inside line comment ignored", "-- comment with OR\na = 1", "OR", true, false},
		{"Word containing or ignored", "order_by = 1", "OR", true, false},
		{"Multi-word IF EXISTS with standard space", "DROP TABLE IF EXISTS tbl", "IF EXISTS", false, true},
		{"Multi-word IF EXISTS with multiple spaces", "DROP TABLE IF    EXISTS tbl", "IF EXISTS", false, true},
		{"Multi-word IF EXISTS with tab", "DROP TABLE IF\tEXISTS tbl", "IF EXISTS", false, true},
		{"Multi-word IF EXISTS with newline", "DROP TABLE IF\nEXISTS tbl", "IF EXISTS", false, true},
		{"Multi-word IF EXISTS with mixed whitespace", "DROP TABLE IF \t\n EXISTS tbl", "IF EXISTS", false, true},
		{"Multi-word IF NOT EXISTS with arbitrary whitespace", "CREATE TABLE IF  NOT \t EXISTS tbl", "IF NOT EXISTS", false, true},
		{"Multi-word WITHIN GROUP with multiple spaces", "PERCENTILE_CONT(0.5) WITHIN    GROUP (ORDER BY x)", "WITHIN GROUP", false, true},
		{"Multi-word WITHIN GROUP with tab", "PERCENTILE_CONT(0.5) WITHIN\tGROUP (ORDER BY x)", "WITHIN GROUP", false, true},
		{"Multi-word WITHIN GROUP with newline", "PERCENTILE_CONT(0.5) WITHIN\nGROUP (ORDER BY x)", "WITHIN GROUP", false, true},
		{"Multi-word ORDER BY with multiple spaces", "SELECT a FROM t ORDER   BY a", "ORDER BY", false, true},
		{"Multi-word ORDER BY with tab", "SELECT a FROM t ORDER\tBY a", "ORDER BY", false, true},
		{"Multi-word IS NOT NULL with arbitrary whitespace", "SELECT a FROM t WHERE x IS  \t NOT \n NULL", "IS NOT NULL", false, true},
		{"Multi-word IS NULL with tab", "SELECT a FROM t WHERE x IS\tNULL", "IS NULL", false, true},
		{"Concatenated words without space should fail", "DROP TABLE IFEXISTS tbl", "IF EXISTS", false, false},
		{"Multi-word inside string literal ignored", "SELECT * FROM t WHERE val = 'IF   EXISTS'", "IF EXISTS", false, false},
		{"Multi-word inside comments ignored", "SELECT * FROM t /* IF    EXISTS */ WHERE id = 1", "IF EXISTS", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainsKeyword(tt.str, tt.keyword, tt.allowInsideParens)
			if got != tt.want {
				t.Errorf("ContainsKeyword(%q, %q, %v) = %v; want %v", tt.str, tt.keyword, tt.allowInsideParens, got, tt.want)
			}
		})
	}
}

func TestHasPrefixKeyword(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		keyword string
		want    bool
	}{
		{"Exact match", "CREATE TABLE t (id INT)", "CREATE TABLE", true},
		{"Multiple spaces", "CREATE   TABLE t (id INT)", "CREATE TABLE", true},
		{"Tabs and newlines", "CREATE \t\n TABLE t (id INT)", "CREATE TABLE", true},
		{"Leading spaces", "   CREATE TABLE t (id INT)", "CREATE TABLE", true},
		{"Leading block comment", "/* comment */ CREATE TABLE t (id INT)", "CREATE TABLE", true},
		{"Leading line comment", "-- line comment\nCREATE TABLE t (id INT)", "CREATE TABLE", true},
		{"Single word prefix", "SELECT * FROM t", "SELECT", true},
		{"Single word prefix with parens", "SELECT(1)", "SELECT", true},
		{"Different keyword", "SELECT * FROM t", "CREATE TABLE", false},
		{"Keyword later in text", "INSERT INTO tbl (id) VALUES (1); CREATE TABLE", "CREATE TABLE", false},
		{"Prefix identifier collision", "CREATING TABLE t", "CREATE TABLE", false},
		{"Empty sql", "", "CREATE TABLE", false},
		{"Empty keyword", "CREATE TABLE t", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasPrefixKeyword(tt.sql, tt.keyword)
			if got != tt.want {
				t.Errorf("HasPrefixKeyword(%q, %q) = %v; want %v", tt.sql, tt.keyword, got, tt.want)
			}
		})
	}
}

func TestIsVolatileSubquery(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want bool
	}{
		{"Empty string", "", false},
		{"Deterministic SELECT", "SELECT AVG(salary) FROM emp WHERE dept_id = 1", false},
		{"Deterministic subquery with string containing RAND", "SELECT * FROM t WHERE name = 'BRAND_NEW'", false},
		{"NEWID call", "SELECT NEWID()", true},
		{"NEWID7 call", "SELECT NEWID7()", true},
		{"RAND call", "SELECT RAND()", true},
		{"GETDATE call", "SELECT GETDATE()", true},
		{"NOW call", "SELECT NOW()", true},
		{"UUID call", "SELECT UUID()", true},
		{"CURRENT_TIMESTAMP identifier", "SELECT CURRENT_TIMESTAMP", true},
		{"Volatile inside subquery with comment", "SELECT /* comment */ NEWID() FROM tbl", true},
		{"Volatile inside string literal is not volatile", "SELECT * FROM t WHERE val = 'NEWID()'", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsVolatileSubquery(tt.sql)
			if got != tt.want {
				t.Errorf("IsVolatileSubquery(%q) = %v; want %v", tt.sql, got, tt.want)
			}
		})
	}
}

// TestTokenizer_Comments_ApostrophesAndKeywords verifies that tokenizer functions
// correctly skip line comments (--) and block comments (/* */) and do not invert
// quote states or misidentify keywords inside comments (AUDIT-FULL-022, AUDIT-FULL-026).
func TestTokenizer_Comments_ApostrophesAndKeywords(t *testing.T) {
	// 1. SplitAndConditions with comment containing apostrophe (don't)
	sqlAnd := "a = 1 -- don't touch\n AND b = 2 /* OR comment */ AND c = 3"
	conds := SplitAndConditions(sqlAnd)
	if len(conds) != 3 {
		t.Errorf("SplitAndConditions: expected 3 conds, got %d: %+v", len(conds), conds)
	}

	// 2. HasTopLevelOr with comment containing OR keyword
	sqlOr := "a = 1 /* OR */ AND b = 2 -- don't OR"
	if HasTopLevelOr(sqlOr) {
		t.Errorf("HasTopLevelOr: expected false when OR is inside comment, got true")
	}

	// 3. ReplaceCorrelatedTokens with comment containing apostrophe
	sqlRepl := "SELECT u.id FROM users u WHERE u.id = 10 -- don't change\n AND u.id = 20"
	replaced := ReplaceCorrelatedTokens(sqlRepl, map[string]string{"u.id": "999"})
	expectedRepl := "SELECT 999 FROM users u WHERE 999 = 10 -- don't change\n AND 999 = 20"
	if replaced != expectedRepl {
		t.Errorf("ReplaceCorrelatedTokens: expected %q, got %q", expectedRepl, replaced)
	}

	// 4. ExtractTopLevelTuples with comment containing parens (AUDIT-FULL-026)
	sqlTuples := "(1, 'a'), -- (skip paren)\n (2, 'b'), /* (skip block) */ (3, 'c')"
	tuples := ExtractTopLevelTuples(sqlTuples)
	if len(tuples) != 3 {
		t.Errorf("ExtractTopLevelTuples: expected 3 tuples, got %d: %+v", len(tuples), tuples)
	}

	// 5. FindTopLevelArithmeticOp with comment dashes
	sqlArith := "a -- subtract\n - b"
	idx, op := FindTopLevelArithmeticOp(sqlArith)
	if op != '-' || idx != len("a -- subtract\n ") {
		t.Errorf("FindTopLevelArithmeticOp: expected operator '-' after newline, got op=%c at idx=%d", op, idx)
	}
}

func TestTrimPrefixKeyword(t *testing.T) {
	tests := []struct {
		sql     string
		prefix  string
		wantRem string
		wantOk  bool
	}{
		{"SAVE TRANSACTION sp1;", "SAVE TRANSACTION", "sp1;", true},
		{"/* comment */ save tran sp_test", "SAVE TRAN", "sp_test", true},
		{"SAVEPOINT sp1", "SAVEPOINT", "sp1", true},
		{"SAVEPOINT", "SAVEPOINT", "", true},
		{"SAVEPOINT_EXTRA", "SAVEPOINT", "SAVEPOINT_EXTRA", false},
		{"ROLLBACK TO SAVEPOINT sp2", "ROLLBACK TO SAVEPOINT", "sp2", true},
		{"SELECT 1", "INSERT", "SELECT 1", false},
	}

	for _, tt := range tests {
		rem, ok := TrimPrefixKeyword(tt.sql, tt.prefix)
		if ok != tt.wantOk || rem != tt.wantRem {
			t.Errorf("TrimPrefixKeyword(%q, %q) = (%q, %v), want (%q, %v)", tt.sql, tt.prefix, rem, ok, tt.wantRem, tt.wantOk)
		}
	}

	// TrimAnyPrefixKeyword
	rem, ok := TrimAnyPrefixKeyword("ROLLBACK WORK TO SAVEPOINT sp3", "ROLLBACK WORK TO SAVEPOINT", "ROLLBACK TO")
	if !ok || rem != "sp3" {
		t.Errorf("TrimAnyPrefixKeyword failed: got (%q, %v), want (sp3, true)", rem, ok)
	}

	rem, ok = TrimAnyPrefixKeyword("ROLLBACK TO sp4", "ROLLBACK WORK TO SAVEPOINT", "ROLLBACK TO")
	if !ok || rem != "sp4" {
		t.Errorf("TrimAnyPrefixKeyword fallback failed: got (%q, %v), want (sp4, true)", rem, ok)
	}
}

