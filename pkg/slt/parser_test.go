package slt_test

import (
	"strings"
	"testing"

	"lionsql/pkg/slt"
)

func TestParser_OnlyIfAndSkipIf(t *testing.T) {
	input := `
# Directive test
onlyif mssql
halt

skipif sqlite
halt

onlyif sqlite
statement ok
CREATE TABLE test_tab(x INT)

skipif sqlite
statement ok
DROP TABLE test_tab

onlyif postgresql
skipif sqlite
query I
SELECT 1
----
1

onlyif sqlite
query I
SELECT 42
----
42
`

	records, err := slt.Parse(strings.NewReader(input), "test.slt")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	// Because target dialect is sqlite:
	// 1. onlyif mssql -> skip halt
	// 2. skipif sqlite -> skip halt
	// 3. onlyif sqlite -> execute statement ok CREATE TABLE
	// 4. skipif sqlite -> skip statement ok DROP TABLE
	// 5. onlyif postgresql + skipif sqlite -> skip query SELECT 1
	// 6. onlyif sqlite -> execute query SELECT 42

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	if records[0].Type != slt.CmdStatementOk || !strings.Contains(records[0].SQL, "CREATE TABLE test_tab") {
		t.Errorf("record 0 mismatch: %+v", records[0])
	}

	if records[1].Type != slt.CmdQuery || !strings.Contains(records[1].SQL, "SELECT 42") {
		t.Errorf("record 1 mismatch: %+v", records[1])
	}
}

func TestParser_MultipleDirectives_OR_Semantics(t *testing.T) {
	// Multiple onlyif lines should match if AT LEAST ONE matches the target dialect
	input := `
onlyif mysql
onlyif sqlite
statement ok
SELECT 100

onlyif oracle
onlyif postgresql
statement ok
SELECT 200
`
	records, err := slt.Parse(strings.NewReader(input), "multi_directive.slt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if !strings.Contains(records[0].SQL, "SELECT 100") {
		t.Errorf("expected SELECT 100, got: %s", records[0].SQL)
	}
}

func TestParser_StatementError_Pattern(t *testing.T) {
	input := `
statement error syntax error
SELECT FROM table

statement error duplicate key
INSERT INTO t VALUES (1)

statement error
SELECT * FROM missing_table
`
	records, err := slt.Parse(strings.NewReader(input), "error_pattern.slt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	if records[0].ExpectedError != "syntax error" {
		t.Errorf("expected 'syntax error', got %q", records[0].ExpectedError)
	}
	if records[1].ExpectedError != "duplicate key" {
		t.Errorf("expected 'duplicate key', got %q", records[1].ExpectedError)
	}
	if records[2].ExpectedError != "" {
		t.Errorf("expected empty pattern, got %q", records[2].ExpectedError)
	}
}

type mockExecutor struct {
	execFunc func(sqlStr string) ([]string, []map[string]any, error)
}

func (m *mockExecutor) Execute(sqlStr string) ([]string, []map[string]any, error) {
	return m.execFunc(sqlStr)
}

func TestRunner_StatementError_Matching(t *testing.T) {
	// 1. Success when error contains expected pattern
	recOk := slt.Record{
		Type:          slt.CmdStatementError,
		SQL:           "SELECT invalid",
		ExpectedError: "syntax error",
	}
	execOk := &mockExecutor{
		execFunc: func(sqlStr string) ([]string, []map[string]any, error) {
			return nil, nil, &customErr{msg: "SYNTAX ERROR: unexpected token near invalid"}
		},
	}
	if err := slt.RunRecord(execOk, recOk); err != nil {
		t.Fatalf("expected pass, got error: %v", err)
	}

	// 2. Failure when statement unexpectedly succeeds
	execSucceeds := &mockExecutor{
		execFunc: func(sqlStr string) ([]string, []map[string]any, error) {
			return []string{"col"}, []map[string]any{{"col": 1}}, nil
		},
	}
	if err := slt.RunRecord(execSucceeds, recOk); err == nil {
		t.Fatal("expected failure when statement error succeeds, got nil")
	}

	// 3. Failure when error message does not match expected pattern
	execMismatch := &mockExecutor{
		execFunc: func(sqlStr string) ([]string, []map[string]any, error) {
			return nil, nil, &customErr{msg: "table not found: users"}
		},
	}
	if err := slt.RunRecord(execMismatch, recOk); err == nil {
		t.Fatal("expected failure when error pattern does not match, got nil")
	}
}

type customErr struct {
	msg string
}

func (e *customErr) Error() string {
	return e.msg
}

func TestRunner_FormatCell_Floats(t *testing.T) {
	tests := []struct {
		val      any
		colType  byte
		expected string
	}{
		{float64(3.14159), 'R', "3.142"},
		{float64(2.0), 'R', "2.000"},
		{float64(42.0), 'I', "42"},
		{float64(42.5), 'I', "42"},
		{float64(100.0), 'T', "100"},
		{float64(100.25), 'T', "100.250"},
		{float32(1.5), 'R', "1.500"},
		{float32(7.0), 'R', "7.000"},
	}

	for _, tc := range tests {
		got := slt.FormatCell(tc.val, tc.colType)
		if got != tc.expected {
			t.Errorf("FormatCell(%v, %c): expected %q, got %q", tc.val, tc.colType, tc.expected, got)
		}
	}
}

func TestParser_SplitRowFields(t *testing.T) {
	input := `
query TT rowsort
SELECT col1, col2
----
'Alice Smith' Engineering
"Bob Builder" "General Sales"
Charlie Operations
`
	records, err := slt.Parse(strings.NewReader(input), "split_test.slt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	expected := []string{
		"Alice Smith", "Engineering",
		"Bob Builder", "General Sales",
		"Charlie", "Operations",
	}
	if len(records[0].ExpectedValues) != len(expected) {
		t.Fatalf("expected %d values, got %d: %v", len(expected), len(records[0].ExpectedValues), records[0].ExpectedValues)
	}
	for i, exp := range expected {
		if records[0].ExpectedValues[i] != exp {
			t.Errorf("cell %d mismatch: expected %q, got %q", i, exp, records[0].ExpectedValues[i])
		}
	}
}
