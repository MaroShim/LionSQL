package runtime_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage/memengine"
)

func TestRuntime_TableVariables(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)
	executor.SetTableManager(eng)

	sql := `
CREATE PROCEDURE TestTableVarProc
AS
BEGIN
    DECLARE @items TABLE (id INT PRIMARY KEY, name VARCHAR(50), price INT);
    INSERT INTO @items VALUES (1, 'Book', 15), (2, 'Pen', 3);
    UPDATE @items SET price = 20 WHERE id = 1;
    DELETE FROM @items WHERE id = 2;
    SELECT id, name, price FROM @items;
END;
`
	prog, err := proc.Compile(sql)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	res, err := executor.Run(prog, "TestTableVarProc", nil, session)
	if err != nil {
		t.Fatalf("executor.Run failed: %v", err)
	}

	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	if fmt.Sprintf("%v", row["name"]) != "Book" || fmt.Sprintf("%v", row["price"]) != "20" {
		t.Fatalf("unexpected row data: %v", row)
	}
}
