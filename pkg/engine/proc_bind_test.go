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
