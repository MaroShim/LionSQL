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
