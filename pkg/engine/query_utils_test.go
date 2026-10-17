package engine_test

import (
	"reflect"
	"testing"

	"sqlion/pkg/engine"
)

func TestSplitStatements_Basic(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "whitespace only",
			input:    "   \t\n  ",
			expected: nil,
		},
		{
			name:     "single statement without semicolon",
			input:    "SELECT * FROM users",
			expected: []string{"SELECT * FROM users"},
		},
		{
			name:     "single statement with semicolon",
			input:    "SELECT * FROM users;",
			expected: []string{"SELECT * FROM users"},
		},
		{
			name:     "multiple statements with semicolons",
			input:    "CREATE TABLE t1 (id INT); INSERT INTO t1 VALUES (1); SELECT * FROM t1;",
			expected: []string{"CREATE TABLE t1 (id INT)", "INSERT INTO t1 VALUES (1)", "SELECT * FROM t1"},
		},
		{
			name:     "multiple statements with whitespace and newlines",
			input:    "\n\nSELECT 1;\n\nSELECT 2;\n\n",
			expected: []string{"SELECT 1", "SELECT 2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := engine.SplitStatements(tc.input)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("SplitStatements(%q)\ngot:  %#v\nwant: %#v", tc.input, got, tc.expected)
			}
		})
	}
}

func TestSplitStatements_QuotesAndComments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "semicolon in single quotes",
			input:    "INSERT INTO messages VALUES ('hello; world'); SELECT 1;",
			expected: []string{"INSERT INTO messages VALUES ('hello; world')", "SELECT 1"},
		},
		{
			name:     "semicolon in double quotes",
			input:    `SELECT "col;1", "col;2" FROM t1; SELECT 2;`,
			expected: []string{`SELECT "col;1", "col;2" FROM t1`, "SELECT 2"},
		},
		{
			name:     "semicolon in line comments",
			input:    "SELECT 1; -- this is a comment; with semicolon\nSELECT 2;",
			expected: []string{"SELECT 1", "-- this is a comment; with semicolon\nSELECT 2"},
		},
		{
			name:     "semicolon in block comments",
			input:    "SELECT 1 /* block comment; here */; SELECT 2;",
			expected: []string{"SELECT 1 /* block comment; here */", "SELECT 2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := engine.SplitStatements(tc.input)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("SplitStatements(%q)\ngot:  %#v\nwant: %#v", tc.input, got, tc.expected)
			}
		})
	}
}

func TestSplitStatements_ProceduresAndBlocks(t *testing.T) {
	procSQL := `
CREATE PROCEDURE TransferFunds
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    SELECT balance FROM accounts WHERE id = @from;
    UPDATE accounts SET balance = balance - @amount WHERE id = @from;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to;
END;
`
	batchSQL := procSQL + "\nSELECT * FROM accounts;\n"

	got := engine.SplitStatements(batchSQL)
	if len(got) != 2 {
		t.Fatalf("expected 2 statements (1 procedure + 1 select), got %d: %#v", len(got), got)
	}

	if got[1] != "SELECT * FROM accounts" {
		t.Fatalf("expected second statement to be SELECT, got: %q", got[1])
	}
}

func BenchmarkSplitStatements_SingleStatement(b *testing.B) {
	query := "SELECT id, name, balance FROM accounts WHERE id = 12345 AND status = 'ACTIVE'"
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		stmts := engine.SplitStatements(query)
		if len(stmts) != 1 {
			b.Fatalf("unexpected stmt count: %d", len(stmts))
		}
	}
}

func BenchmarkSplitStatements_MultiStatement(b *testing.B) {
	query := "BEGIN TRANSACTION; UPDATE accounts SET balance = 100 WHERE id = 1; COMMIT;"
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		stmts := engine.SplitStatements(query)
		if len(stmts) != 3 {
			b.Fatalf("unexpected stmt count: %d", len(stmts))
		}
	}
}
