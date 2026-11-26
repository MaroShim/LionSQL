package sqlutil_test

import (
	"testing"

	"lionsql/pkg/sqlutil"
)

func TestEscapedBracket_EndToEndSQLParsing(t *testing.T) {
	// 1. SplitSelectClauses with escaped bracket in projections and from clause
	sql1 := "SELECT [col]]1], [user]]name], [col, with, commas]]and]brackets] FROM [my]]db].[dbo].[my]]table] WHERE [status]]flag] = 1"
	clauses := sqlutil.SplitSelectClauses(sql1)
	if clauses.Where != "[status]]flag] = 1" {
		t.Fatalf("unexpected Where clause: %q", clauses.Where)
	}

	// 2. SplitTopLevel with comma respecting escaped brackets
	proj := "[col]]1], [user]]name], [col, with, commas]]and]brackets]"
	parts := sqlutil.SplitTopLevel(proj, ',')
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %#v", len(parts), parts)
	}
	if sqlutil.CleanIdentifier(parts[0]) != "col]1" {
		t.Errorf("part 0 expected 'col]1', got %q", sqlutil.CleanIdentifier(parts[0]))
	}
	if sqlutil.CleanIdentifier(parts[1]) != "user]name" {
		t.Errorf("part 1 expected 'user]name', got %q", sqlutil.CleanIdentifier(parts[1]))
	}
	if sqlutil.CleanIdentifier(parts[2]) != "col, with, commas]and]brackets" {
		t.Errorf("part 2 expected 'col, with, commas]and]brackets', got %q", sqlutil.CleanIdentifier(parts[2]))
	}

	// 3. FindTopLevelKeyword with escaped bracket containing keywords
	sql2 := "SELECT [FROM]]AND]WHERE] FROM [users]"
	fromIdx := sqlutil.FindTopLevelKeyword(sql2, "FROM")
	if fromIdx == -1 || fromIdx < 20 {
		t.Fatalf("expected FROM keyword to be found outside bracket, got idx %d", fromIdx)
	}
}
