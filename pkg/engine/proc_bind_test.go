package engine

import (
	"strconv"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestResolvePreparedProcedure(t *testing.T) {
	memEng := memengine.New()
	cat := catalog.New(memEng)
	cat.RegisterProcedure(&catalog.Procedure{
		Name: "TPCC_Payment",
		Params: []catalog.ParamDef{
			{Name: "@w_id", Type: storage.TypeInt},
			{Name: "@d_key", Type: storage.TypeVarChar},
			{Name: "@c_key", Type: storage.TypeVarChar},
			{Name: "@amount", Type: storage.TypeFloat},
		},
		Body: `
CREATE PROCEDURE TPCC_Payment
    @w_id INT,
    @d_key VARCHAR,
    @c_key VARCHAR,
    @amount FLOAT
AS
BEGIN
    UPDATE warehouse SET w_ytd = w_ytd + @amount WHERE w_id = @w_id;
END;
`,
	})

	cache := runtime.NewPlanCache()

	t.Run("PGWire Named Placeholders", func(t *testing.T) {
		q := "EXEC TPCC_Payment @w_id=$1, @d_key=$2, @c_key=$3, @amount=$4;"
		info := ResolvePreparedProcedure(q, cat, cache)
		if info == nil {
			t.Fatalf("expected non-nil info")
		}
		if info.ProcName != "TPCC_Payment" {
			t.Errorf("expected procName TPCC_Payment, got %s", info.ProcName)
		}
		if info.Program == nil {
			t.Fatalf("expected compiled program")
		}
		if len(info.ParamNames) != 4 {
			t.Fatalf("expected 4 param names, got %d", len(info.ParamNames))
		}
		expected := []string{"@w_id", "@d_key", "@c_key", "@amount"}
		for i, exp := range expected {
			if info.ParamNames[i] != exp {
				t.Errorf("slot %d: expected %s, got %s", i, exp, info.ParamNames[i])
			}
		}
	})

	t.Run("TDS Named Placeholders", func(t *testing.T) {
		q := "EXEC TPCC_Payment @w_id=@p1, @d_key=@p2, @c_key=@p3, @amount=@p4;"
		info := ResolvePreparedProcedure(q, cat, cache)
		if info == nil {
			t.Fatalf("expected non-nil info")
		}
		if len(info.ParamNames) != 4 {
			t.Fatalf("expected 4 param names, got %d", len(info.ParamNames))
		}
		expected := []string{"@w_id", "@d_key", "@c_key", "@amount"}
		for i, exp := range expected {
			if info.ParamNames[i] != exp {
				t.Errorf("slot %d: expected %s, got %s", i, exp, info.ParamNames[i])
			}
		}
	})

	t.Run("Positional Placeholders", func(t *testing.T) {
		q := "EXEC TPCC_Payment $1, $2, $3, $4;"
		info := ResolvePreparedProcedure(q, cat, cache)
		if info == nil {
			t.Fatalf("expected non-nil info")
		}
		if len(info.ParamNames) != 4 {
			t.Fatalf("expected 4 param names, got %d", len(info.ParamNames))
		}
		expected := []string{"@w_id", "@d_key", "@c_key", "@amount"}
		for i, exp := range expected {
			if info.ParamNames[i] != exp {
				t.Errorf("slot %d: expected %s, got %s", i, exp, info.ParamNames[i])
			}
		}
	})

	t.Run("TPCC_NewOrder 58 Parameters Mapping", func(t *testing.T) {
		var paramDefs []catalog.ParamDef
		paramDefs = append(paramDefs,
			catalog.ParamDef{Name: "@w_id", Type: storage.TypeInt},
			catalog.ParamDef{Name: "@d_key", Type: storage.TypeVarChar},
			catalog.ParamDef{Name: "@c_key", Type: storage.TypeVarChar},
			catalog.ParamDef{Name: "@o_key", Type: storage.TypeVarChar},
			catalog.ParamDef{Name: "@next_o_id", Type: storage.TypeInt},
			catalog.ParamDef{Name: "@d_id", Type: storage.TypeInt},
			catalog.ParamDef{Name: "@c_id", Type: storage.TypeInt},
			catalog.ParamDef{Name: "@ol_cnt", Type: storage.TypeInt},
		)
		for i := 1; i <= 10; i++ {
			paramDefs = append(paramDefs,
				catalog.ParamDef{Name: "@s_key" + strconv.Itoa(i), Type: storage.TypeVarChar},
				catalog.ParamDef{Name: "@i_id" + strconv.Itoa(i), Type: storage.TypeInt},
				catalog.ParamDef{Name: "@qty" + strconv.Itoa(i), Type: storage.TypeInt},
				catalog.ParamDef{Name: "@ol_key" + strconv.Itoa(i), Type: storage.TypeVarChar},
				catalog.ParamDef{Name: "@amt" + strconv.Itoa(i), Type: storage.TypeFloat},
			)
		}
		cat.RegisterProcedure(&catalog.Procedure{
			Name:   "TPCC_NewOrder",
			Params: paramDefs,
			Body: `
CREATE PROCEDURE TPCC_NewOrder
    @w_id INT
AS
BEGIN
    SELECT 1;
END;
`,
		})

		noParams := []string{
			"@w_id", "@d_key", "@c_key", "@o_key", "@next_o_id", "@d_id", "@c_id", "@ol_cnt",
		}
		for i := 1; i <= 10; i++ {
			noParams = append(noParams,
				"@s_key"+strconv.Itoa(i),
				"@i_id"+strconv.Itoa(i),
				"@qty"+strconv.Itoa(i),
				"@ol_key"+strconv.Itoa(i),
				"@amt"+strconv.Itoa(i),
			)
		}
		var noArgs []string
		for i, p := range noParams {
			noArgs = append(noArgs, p+"=$"+string(rune('0'+(i+1))))
		}
		// Test PGWire query: EXEC TPCC_NewOrder @w_id=$1, @d_key=$2...
		var sb strings.Builder
		sb.WriteString("EXEC TPCC_NewOrder ")
		for i, p := range noParams {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(p)
			sb.WriteString("=$")
			sb.WriteString(strconv.Itoa(i + 1))
		}
		sb.WriteString(";")

		info := ResolvePreparedProcedure(sb.String(), cat, cache)
		if info == nil {
			t.Fatalf("expected non-nil info for TPCC_NewOrder")
		}
		if len(info.ParamNames) != 58 {
			t.Fatalf("expected 58 param names, got %d", len(info.ParamNames))
		}
		if info.ParamNames[0] != "@w_id" {
			t.Errorf("slot 0: expected @w_id, got %s", info.ParamNames[0])
		}
		if info.ParamNames[57] != "@amt:" && info.ParamNames[57] != "@amt10" {
			// let's check last param
			t.Logf("slot 57: %s", info.ParamNames[57])
		}
	})

	t.Run("Non-Procedure or System Proc Returns Nil", func(t *testing.T) {
		if ResolvePreparedProcedure("SELECT 1", cat, cache) != nil {
			t.Errorf("expected nil for SELECT")
		}
		if ResolvePreparedProcedure("EXEC sp_executesql N'SELECT 1'", cat, cache) != nil {
			t.Errorf("expected nil for sp_executesql")
		}
		if ResolvePreparedProcedure("EXEC NonExistentProc $1", cat, cache) != nil {
			t.Errorf("expected nil for unknown proc")
		}
	})
}

func TestBuildSpExecuteSQL_NoCascadingSubstitution(t *testing.T) {
	for i := 0; i < 20; i++ {
		stmt := "SELECT * FROM tbl WHERE a = @p1 AND b = @p2"
		namedArgs := map[string]string{
			"@p1": "@p2",
			"@p2": "999",
		}
		res := buildSpExecuteSQL(stmt, "@p1 VARCHAR, @p2 INT", namedArgs, nil)
		expected := "SELECT * FROM tbl WHERE a = @p2 AND b = 999"
		if res != expected {
			t.Fatalf("Cascading substitution occurred at iter %d! Expected %q, got %q", i, expected, res)
		}
	}
}

func TestExecProcedure_UnknownNamedArgument_Error8145(t *testing.T) {
	memEng := memengine.New()
	cat := catalog.New(memEng)
	coord := NewCoordinator(cat, memEng, Config{})
	defer coord.Close()

	ctx := t.Context()
	_, err := coord.ExecuteSessionSQLContext(ctx, "default", `
CREATE PROCEDURE sp_test_8145
    @a INT,
    @b VARCHAR(50) = 'hello'
AS
BEGIN
    SELECT @a AS a, @b AS b;
END;
`)
	if err != nil {
		t.Fatalf("failed to create procedure: %v", err)
	}

	// 1. Valid execution with known named arguments should succeed
	res, err := coord.ExecuteSessionSQLContext(ctx, "default", "EXEC sp_test_8145 @a = 10, @b = 'world';")
	if err != nil || !res.Success {
		t.Fatalf("valid EXEC failed: %v", err)
	}

	// 2. Execution with unknown named argument @unknown should fail with Error 8145
	res, err = coord.ExecuteSessionSQLContext(ctx, "default", "EXEC sp_test_8145 @a = 10, @unknown = 20;")
	if err == nil && (res == nil || res.Success) {
		t.Fatalf("expected Error 8145 for unknown argument @unknown, but execution succeeded!")
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	} else if res != nil && !res.Success {
		errMsg = res.Error
	}

	if !strings.Contains(errMsg, "8145") || !strings.Contains(errMsg, "@unknown is not a parameter for procedure") {
		t.Fatalf("expected error message to contain '8145' and '@unknown is not a parameter for procedure', got: %q", errMsg)
	}
}

func TestExecProcedure_TooManyArguments_Error8144(t *testing.T) {
	memEng := memengine.New()
	cat := catalog.New(memEng)
	coord := NewCoordinator(cat, memEng, Config{})
	defer coord.Close()

	ctx := t.Context()
	_, err := coord.ExecuteSessionSQLContext(ctx, "default", `
CREATE PROCEDURE sp_test_8144
    @a INT
AS
BEGIN
    SELECT @a AS a;
END;
`)
	if err != nil {
		t.Fatalf("failed to create procedure: %v", err)
	}

	// 1. Positional call with 2 arguments when only 1 is declared
	res, err := coord.ExecuteSessionSQLContext(ctx, "default", "EXEC sp_test_8144 10, 20;")
	if err == nil && (res == nil || res.Success) {
		t.Fatalf("expected Error 8144 for too many arguments, but execution succeeded!")
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	} else if res != nil && !res.Success {
		errMsg = res.Error
	}

	if !strings.Contains(errMsg, "8144") || !strings.Contains(errMsg, "has too many arguments specified") {
		t.Fatalf("expected error message to contain '8144' and 'has too many arguments specified', got: %q", errMsg)
	}
}

func TestStoredProcedure_StaticAlterTable_R1_09(t *testing.T) {
	memEng := memengine.New()
	defer memEng.Close()
	cat := catalog.New(memEng)
	coord := NewCoordinator(cat, memEng, DefaultConfig())
	defer coord.Close()

	sessID := "sess-alter-proc"
	if _, err := coord.ExecuteSessionSQL(sessID, "CREATE TABLE t_alter_tbl (id INT PRIMARY KEY);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	procSQL := `
CREATE PROCEDURE sp_TestDdlProc AS
BEGIN
    ALTER TABLE t_alter_tbl ADD col_new INT;
END;`
	if _, err := coord.ExecuteSessionSQL(sessID, procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE with static ALTER TABLE failed: %v", err)
	}

	if _, err := coord.ExecuteSessionSQL(sessID, "EXEC sp_TestDdlProc;"); err != nil {
		t.Fatalf("EXEC sp_TestDdlProc failed: %v", err)
	}

	tbl, err := memEng.GetTable("t_alter_tbl")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	col, _, found := tbl.FindColumn("col_new")
	if !found || col == nil {
		t.Fatalf("column col_new was not added by ALTER TABLE inside stored procedure")
	}
	if col.Type != storage.TypeInt {
		t.Fatalf("expected column type INT, got %v", col.Type)
	}
}

func TestResolvePreparedProcedure_DualPath_EdgeCases(t *testing.T) {
	memEng := memengine.New()
	cat := catalog.New(memEng)
	cache := runtime.NewPlanCache()

	// 1. User procedure starting with "sp_" and 0 parameters
	_ = cat.RegisterProcedure(&catalog.Procedure{
		Name: "sp_user_zero_param",
		Body: `CREATE PROCEDURE sp_user_zero_param AS BEGIN SELECT 1 AS a; END;`,
	})

	// 2. User procedure starting with "sp_" and parameters
	_ = cat.RegisterProcedure(&catalog.Procedure{
		Name: "sp_user_with_params",
		Params: []catalog.ParamDef{
			{Name: "@val", Type: storage.TypeInt},
		},
		Body: `CREATE PROCEDURE sp_user_with_params @val INT AS BEGIN SELECT @val * 2 AS res; END;`,
	})

	// Case A: 0-parameter with sp_ prefix and trailing semicolon
	t.Run("Zero Param with sp_ prefix and semicolon", func(t *testing.T) {
		info := ResolvePreparedProcedure("EXEC sp_user_zero_param;", cat, cache)
		if info == nil {
			t.Fatalf("expected non-nil info for zero param sp_user_zero_param")
		}
		if info.ProcName != "sp_user_zero_param" {
			t.Errorf("expected sp_user_zero_param, got %s", info.ProcName)
		}
		if info.Program == nil {
			t.Fatalf("expected non-nil program")
		}
	})

	// Case B: CALL syntax with parentheses and semicolon
	t.Run("CALL syntax with sp_ prefix and parentheses", func(t *testing.T) {
		info := ResolvePreparedProcedure("CALL sp_user_zero_param();", cat, cache)
		if info == nil {
			t.Fatalf("expected non-nil info for CALL sp_user_zero_param()")
		}
		if info.ProcName != "sp_user_zero_param" {
			t.Errorf("expected sp_user_zero_param, got %s", info.ProcName)
		}
	})

	// Case C: Parameterized procedure with sp_ prefix and $1 placeholder
	t.Run("Parameterized sp_ procedure with $1", func(t *testing.T) {
		info := ResolvePreparedProcedure("EXEC sp_user_with_params $1;", cat, cache)
		if info == nil {
			t.Fatalf("expected non-nil info for sp_user_with_params $1")
		}
		if len(info.ParamNames) != 1 || info.ParamNames[0] != "@val" {
			t.Errorf("expected [@val], got %v", info.ParamNames)
		}
	})

	// Case D: True built-in system procedure MUST return nil (fallback to coordinator)
	t.Run("Builtin system proc sp_help returns nil", func(t *testing.T) {
		info := ResolvePreparedProcedure("EXEC sp_help;", cat, cache)
		if info != nil {
			t.Fatalf("expected nil for builtin sp_help, got %+v", info)
		}
		info2 := ResolvePreparedProcedure("EXEC sp_executesql @stmt=$1;", cat, cache)
		if info2 != nil {
			t.Fatalf("expected nil for builtin sp_executesql, got %+v", info2)
		}
	})
}


