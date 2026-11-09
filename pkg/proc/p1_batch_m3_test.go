package proc_test

import (
	"testing"

	"lionsql/pkg/proc"
)

// TestP1_BatchM3_JoinWithCTEAndSelectInto tests Defect 6-4 and Defect 6-5.
func TestP1_BatchM3_JoinWithCTEAndSelectInto(t *testing.T) {
	// Defect 6-5: JOIN (WITH cte AS (...) SELECT ...) should parse without 'expected projection expression, got WITH'
	t.Run("Defect_6_5_JoinWithCTE", func(t *testing.T) {
		query := "SELECT a.id FROM a JOIN (WITH c AS (SELECT id FROM b) SELECT id FROM c) x ON a.id = x.id"
		stmt, err := proc.ParseSQL(query)
		if err != nil {
			t.Fatalf("failed to parse query with JOIN (WITH ...): %v", err)
		}
		if stmt == nil {
			t.Fatalf("expected non-nil statement")
		}
	})

	// Defect 6-4: SELECT col INTO table FROM src in a procedure / block statement should parse into table statement or general select rather than failing on non-variable
	t.Run("Defect_6_4_SelectIntoTable", func(t *testing.T) {
		procSQL := `CREATE PROCEDURE sp_test_into AS BEGIN SELECT id INTO #target_tbl FROM src; END;`
		parser := proc.NewParser(proc.NewLexer(procSQL))
		stmt, err := parser.ParseProcedure()
		if err != nil {
			t.Fatalf("failed to parse procedure with SELECT ... INTO #target_tbl: %v", err)
		}
		if stmt == nil {
			t.Fatalf("expected non-nil statement")
		}
	})
}
