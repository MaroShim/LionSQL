package proc_test

import (
	"strings"
	"testing"

	"sqlion/pkg/proc"
	_ "sqlion/pkg/proc/parser/antlr"
)

func TestCompiler_ProcedureBytecode(t *testing.T) {
	tsql := `
CREATE PROCEDURE CalculateInterest
    @accId INT,
    @rate FLOAT
AS
BEGIN
    DECLARE @bal INT;
    DECLARE @interest FLOAT;
    SELECT balance INTO @bal FROM accounts WHERE id = @accId;
    IF @bal > 1000
    BEGIN
        SET @interest = @bal * @rate;
        UPDATE accounts SET balance = balance + @interest WHERE id = @accId;
        PRINT 'Interest applied';
        RETURN @interest;
    END
    ELSE
    BEGIN
        PRINT 'No interest applied';
        RETURN 0;
    END
END;
`
	prog, err := proc.Compile(tsql)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	if prog.Name != "CalculateInterest" {
		t.Errorf("expected name CalculateInterest, got %s", prog.Name)
	}
	if len(prog.Params) != 2 {
		t.Errorf("expected 2 params, got %d", len(prog.Params))
	}
	if len(prog.Instructions) == 0 {
		t.Fatalf("expected non-empty instructions")
	}

	dis := prog.Disassemble()
	if !strings.Contains(dis, "SELECT_INTO") || !strings.Contains(dis, "JUMP_IF_FALSE") {
		t.Errorf("disassembly missing expected opcodes:\n%s", dis)
	}
}

func TestCompiler_FunctionBytecode(t *testing.T) {
	fnSQL := `
CREATE FUNCTION dbo.fn_AddTax(@Amount FLOAT, @Rate FLOAT)
RETURNS FLOAT
AS
BEGIN
    RETURN @Amount * (1 + @Rate);
END
`
	prog, err := proc.Compile(fnSQL)
	if err != nil {
		t.Fatalf("Compile function failed: %v", err)
	}

	if prog.Type != "FUNCTION" {
		t.Errorf("expected Type FUNCTION, got %s", prog.Type)
	}
	if len(prog.Instructions) == 0 {
		t.Fatalf("expected non-empty instructions")
	}
}

func TestCompiler_TriggerBytecode(t *testing.T) {
	trgSQL := `
CREATE TRIGGER trg_AuditOrders
ON orders
AFTER INSERT
AS
BEGIN
    DECLARE @id INT;
    SET @id = (SELECT id FROM inserted);
    INSERT INTO order_audit (order_id) VALUES (@id);
END
`
	prog, err := proc.Compile(trgSQL)
	if err != nil {
		t.Fatalf("Compile trigger failed: %v", err)
	}

	if prog.Type != "TRIGGER" {
		t.Errorf("expected Type TRIGGER, got %s", prog.Type)
	}
	if prog.Table != "orders" {
		t.Errorf("expected Table orders, got %s", prog.Table)
	}
}

func TestCompiler_AntlrFallbackProcedure(t *testing.T) {
	// A T-SQL procedure with MSSQL specific clauses (WITH RECOMPILE) that fail in handwritten Pratt parser
	// but succeed through the ANTLR fallback parser!
	tsql := `
CREATE PROCEDURE dbo.ProcessRecompileBonus
    @empId INT,
    @bonus INT
WITH RECOMPILE
AS
BEGIN
    UPDATE employees SET salary = salary + @bonus WHERE id = @empId;
    RETURN 0;
END;
`
	prog, err := proc.Compile(tsql)
	if err != nil {
		t.Fatalf("Compile with ANTLR fallback failed: %v", err)
	}

	if prog.Name != "ProcessRecompileBonus" {
		t.Errorf("expected name ProcessRecompileBonus, got %s", prog.Name)
	}
	if len(prog.Params) != 2 {
		t.Errorf("expected 2 params, got %d", len(prog.Params))
	}
	if len(prog.Instructions) == 0 {
		t.Fatalf("expected non-empty instructions from compiled ANTLR AST")
	}
}

