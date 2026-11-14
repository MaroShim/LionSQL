package runtime_test

import (
	"strings"
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage/memengine"
)

// TestVDBE_SelectAssign_Bytecode verifies that table-less SELECT @var = expr compiles to OpStoreVar and emits zero OpResultRow.
func TestVDBE_SelectAssign_Bytecode(t *testing.T) {
	sql := `
CREATE PROCEDURE sp_test_assign
AS
BEGIN
    DECLARE @x INT;
    DECLARE @y INT;
    SELECT @x = 100, @y = 200;
END;
`
	p := proc.NewParser(proc.NewLexer(sql))
	procStmt, err := p.ParseProcedure()
	if err != nil {
		t.Fatalf("failed to parse procedure: %v", err)
	}

	prog, err := proc.CompileProcedure(procStmt)
	if err != nil {
		t.Fatalf("failed to compile procedure: %v", err)
	}

	// Verify instructions: should have OpStoreVar for @x and @y, and NO OpResultRow
	hasStoreVar := 0
	hasResultRow := false
	for _, inst := range prog.Instructions {
		if inst.Op == proc.OpStoreVar {
			hasStoreVar++
		}
		if inst.Op == proc.OpResultRow {
			hasResultRow = true
		}
	}

	if hasStoreVar < 2 {
		t.Fatalf("expected at least 2 OpStoreVar instructions, got %d", hasStoreVar)
	}
	if hasResultRow {
		t.Fatalf("expected 0 OpResultRow instructions for variable assignment, got OpResultRow")
	}

	// Execute on runtime Executor
	eng := memengine.New()
	defer eng.Close()
	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer session.Close()

	exec := runtime.NewExecutor(nil)
	res, err := exec.Run(prog, procStmt.Name, nil, session)
	if err != nil {
		t.Fatalf("unexpected runtime error: %v", err)
	}
	if len(res.Rows) != 0 || len(res.Columns) != 0 {
		t.Fatalf("expected 0 rows/columns, got %d rows", len(res.Rows))
	}
	if val, ok := res.Vars["x"]; !ok || val != int64(100) {
		t.Fatalf("expected var x = 100, got %v", val)
	}
	if val, ok := res.Vars["y"]; !ok || val != int64(200) {
		t.Fatalf("expected var y = 200, got %v", val)
	}
}

// TestVDBE_SelectAssign_Msg141 verifies that mixing variable assignment and data retrieval raises Msg 141 error.
func TestVDBE_SelectAssign_Msg141(t *testing.T) {
	sql := `
CREATE PROCEDURE sp_test_invalid
AS
BEGIN
    DECLARE @x INT;
    SELECT @x = 100, 200;
END;
`
	p := proc.NewParser(proc.NewLexer(sql))
	_, err := p.ParseProcedure()
	if err == nil {
		t.Fatalf("expected parser to raise Msg 141 error when mixing assignment and data retrieval")
	}
	if !strings.Contains(err.Error(), "Msg 141") {
		t.Fatalf("expected error containing 'Msg 141', got: %v", err)
	}
}
