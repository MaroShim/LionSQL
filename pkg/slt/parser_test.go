package slt

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSplitRowFields(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		numCols int
		want    []string
	}{
		{
			name:    "single column",
			line:    "hello world",
			numCols: 1,
			want:    []string{"hello world"},
		},
		{
			name:    "tab separated",
			line:    "col1\tcol 2 has spaces\tcol3",
			numCols: 3,
			want:    []string{"col1", "col 2 has spaces", "col3"},
		},
		{
			name:    "space separated matching numCols",
			line:    "1 2 3",
			numCols: 3,
			want:    []string{"1", "2", "3"},
		},
		{
			name:    "more tokens than numCols preserves space in last column",
			line:    "3 SELECT * FROM nowhere",
			numCols: 2,
			want:    []string{"3", "SELECT * FROM nowhere"},
		},
		{
			name:    "empty line",
			line:    "   ",
			numCols: 2,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitRowFields(tt.line, tt.numCols)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitRowFields(%q, %d) = %v; want %v", tt.line, tt.numCols, got, tt.want)
			}
		})
	}
}

func TestParseFile_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "test.slt")
	content := `# Sample SLT
statement ok
CREATE TABLE sample (id INT, val VARCHAR);

statement error
INVALID SQL SYNTAX;

query IT rowsort
SELECT id, val FROM sample;
----
1 first
2 second

query I
SELECT 10;
----
10

halt
statement ok
SELECT 'unreached';
`
	if err := os.WriteFile(testPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp slt: %v", err)
	}

	records, err := ParseFile(testPath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}

	if len(records) != 5 {
		t.Fatalf("expected 5 records before halt, got %d", len(records))
	}

	if records[0].Type != CmdStatementOk || records[0].SQL != "CREATE TABLE sample (id INT, val VARCHAR);" {
		t.Errorf("unexpected record 0: %+v", records[0])
	}
	if records[1].Type != CmdStatementError || records[1].SQL != "INVALID SQL SYNTAX;" {
		t.Errorf("unexpected record 1: %+v", records[1])
	}
	if records[2].Type != CmdQuery || records[2].TypeSignature != "IT" || records[2].Sort != SortRowsort {
		t.Errorf("unexpected record 2: %+v", records[2])
	}
	expectedVals := []string{"1", "first", "2", "second"}
	if !reflect.DeepEqual(records[2].ExpectedValues, expectedVals) {
		t.Errorf("unexpected expected values: got %v, want %v", records[2].ExpectedValues, expectedVals)
	}
	if records[4].Type != CmdHalt {
		t.Errorf("expected record 4 to be CmdHalt, got %v", records[4].Type)
	}
}

func TestSortQueryResults(t *testing.T) {
	values := []string{"2", "beta", "1", "alpha"}
	
	// nosort preserves original order
	nosorted := SortResults(values, 2, SortNosort)
	if !reflect.DeepEqual(nosorted, values) {
		t.Errorf("SortNosort altered values: got %v", nosorted)
	}

	// rowsort sorts whole rows (1 alpha before 2 beta)
	rowsorted := SortResults(values, 2, SortRowsort)
	wantRow := []string{"1", "alpha", "2", "beta"}
	if !reflect.DeepEqual(rowsorted, wantRow) {
		t.Errorf("SortRowsort failed: got %v, want %v", rowsorted, wantRow)
	}

	// valuesort sorts all cells individually
	valsorted := SortResults(values, 2, SortValuesort)
	wantVal := []string{"1", "2", "alpha", "beta"}
	if !reflect.DeepEqual(valsorted, wantVal) {
		t.Errorf("SortValuesort failed: got %v, want %v", valsorted, wantVal)
	}
}
