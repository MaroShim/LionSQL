package engine_test

import (
	"strings"
	"testing"
)

// TestExec_DefaultKeywordArgument verifies that passing the DEFAULT keyword as an EXEC argument
// binds the declared default value of the procedure parameter instead of binding the string literal "default".
// It also verifies that passing DEFAULT to a parameter without a declared default raises an error (Msg 8178).
func TestExec_DefaultKeywordArgument(t *testing.T) {
	coord, cleanup := newCompatMatrixTestCoord(t)
	defer cleanup()

	// Procedure with mixed parameters: @a has no default, @b has default 10, @c has default 'x'
	createProcSQL := `
		CREATE PROCEDURE p_exec_def
			@a INT,
			@b INT = 10,
			@c VARCHAR(20) = 'x'
		AS
		BEGIN
			SELECT @a + @b AS total, @c AS tag;
		END;
	`
	if _, err := coord.ExecuteSQL(createProcSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	// 1. Positional EXEC with DEFAULT for @b and @c
	t.Run("Positional_DefaultKeyword", func(t *testing.T) {
		res, err := coord.ExecuteSQL("EXEC p_exec_def 5, DEFAULT, DEFAULT;")
		if err != nil {
			t.Fatalf("EXEC p_exec_def with DEFAULT failed: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(res.Rows))
		}
		if res.Rows[0]["total"] != int64(15) {
			t.Errorf("expected total = 15, got %v (%T)", res.Rows[0]["total"], res.Rows[0]["total"])
		}
		if res.Rows[0]["tag"] != "x" {
			t.Errorf("expected tag = 'x', got %v (%T)", res.Rows[0]["tag"], res.Rows[0]["tag"])
		}
	})

	// 2. Named argument EXEC with @b = DEFAULT
	t.Run("NamedArg_DefaultKeyword", func(t *testing.T) {
		res, err := coord.ExecuteSQL("EXEC p_exec_def @a = 7, @b = DEFAULT, @c = 'custom';")
		if err != nil {
			t.Fatalf("EXEC p_exec_def with named DEFAULT failed: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(res.Rows))
		}
		if res.Rows[0]["total"] != int64(17) {
			t.Errorf("expected total = 17, got %v (%T)", res.Rows[0]["total"], res.Rows[0]["total"])
		}
		if res.Rows[0]["tag"] != "custom" {
			t.Errorf("expected tag = 'custom', got %v (%T)", res.Rows[0]["tag"], res.Rows[0]["tag"])
		}
	})

	// 3. Error case: passing DEFAULT to a parameter that has no default (@a)
	t.Run("Error_NoDefaultDeclared", func(t *testing.T) {
		_, err := coord.ExecuteSQL("EXEC p_exec_def DEFAULT, DEFAULT, DEFAULT;")
		if err == nil {
			t.Fatalf("expected error when passing DEFAULT to parameter without default, but got nil")
		}
		if !strings.Contains(err.Error(), "DEFAULT") {
			t.Errorf("expected error message mentioning DEFAULT, got: %v", err)
		}
	})

	// 4. Nested procedure invocation inside another procedure (VDBE path)
	t.Run("NestedVDBE_DefaultKeyword", func(t *testing.T) {
		createCallerSQL := `
			CREATE PROCEDURE p_exec_def_caller
			AS
			BEGIN
				EXEC p_exec_def 20, DEFAULT, 'from_caller';
			END;
		`
		if _, err := coord.ExecuteSQL(createCallerSQL); err != nil {
			t.Fatalf("CREATE PROCEDURE caller failed: %v", err)
		}
		res, err := coord.ExecuteSQL("EXEC p_exec_def_caller;")
		if err != nil {
			t.Fatalf("EXEC p_exec_def_caller failed: %v", err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(res.Rows))
		}
		if res.Rows[0]["total"] != int64(30) {
			t.Errorf("expected total = 30, got %v (%T)", res.Rows[0]["total"], res.Rows[0]["total"])
		}
		if res.Rows[0]["tag"] != "from_caller" {
			t.Errorf("expected tag = 'from_caller', got %v (%T)", res.Rows[0]["tag"], res.Rows[0]["tag"])
		}
	})
}
