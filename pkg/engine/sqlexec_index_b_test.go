package engine

import (
	"fmt"
	"testing"

	"lionsql/pkg/proc"
)

func TestParseDecimalPrecisionScale(t *testing.T) {
	tests := []struct {
		input string
		wantP int
		wantS int
	}{
		{"DECIMAL(10, 2)", 10, 2},
		{"NUMERIC(18, 4)", 18, 4},
		{"DECIMAL(8)", 8, 0},
		{"DECIMAL", 18, 2},
	}

	for _, tt := range tests {
		sql := fmt.Sprintf("CREATE TABLE t (col %s)", tt.input)
		stmt, err := proc.NewParser(proc.NewLexer(sql)).ParseCreateTable()
		if err != nil || len(stmt.Columns) != 1 {
			t.Fatalf("ParseCreateTable(%q) failed: %v", sql, err)
		}
		p, s := stmt.Columns[0].Precision, stmt.Columns[0].Scale
		if p != tt.wantP || s != tt.wantS {
			t.Errorf("ParseCreateTable(%q) = (%d, %d); want (%d, %d)", tt.input, p, s, tt.wantP, tt.wantS)
		}
	}
}

func TestParsePivotQuery_ParenthesisMatching(t *testing.T) {
	query := "SELECT * FROM (SELECT Year, Amount FROM Sales) AS src PIVOT (SUM(Amount) FOR Year IN ([2020], [2021])) AS pvt"
	parsed, err := parsePivotQuery(query)
	if err != nil {
		t.Fatalf("parsePivotQuery failed: %v", err)
	}
	if parsed.AggFunc != "SUM" {
		t.Errorf("expected AggFunc SUM, got %q", parsed.AggFunc)
	}
	if parsed.AggCol != "Amount" {
		t.Errorf("expected AggCol Amount, got %q", parsed.AggCol)
	}
	if parsed.PivotCol != "Year" {
		t.Errorf("expected PivotCol Year, got %q", parsed.PivotCol)
	}
}

func TestParseUnpivotQuery_ParenthesisMatching(t *testing.T) {
	query := "SELECT * FROM SalesTable UNPIVOT (Amount FOR Quarter IN (Q1, Q2, Q3, Q4)) AS unpvt"
	parsed, err := parseUnpivotQuery(query)
	if err != nil {
		t.Fatalf("parseUnpivotQuery failed: %v", err)
	}
	if parsed.ValueCol != "Amount" {
		t.Errorf("expected ValueCol Amount, got %q", parsed.ValueCol)
	}
	if parsed.NameCol != "Quarter" {
		t.Errorf("expected NameCol Quarter, got %q", parsed.NameCol)
	}
}
