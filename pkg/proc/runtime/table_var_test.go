package runtime_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
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

func TestRuntime_TableVariables_TightLoopRedeclare(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)
	executor.SetTableManager(eng)

	sql := `
CREATE PROCEDURE TestTableVarTightLoop
AS
BEGIN
    DECLARE @i INT;
    SET @i = 0;
    DECLARE @total INT;
    SET @total = 0;
    WHILE @i < 200
    BEGIN
        DECLARE @tmp TABLE (val INT);
        INSERT INTO @tmp VALUES (@i);
        SET @total = @total + (SELECT val FROM @tmp);
        SET @i = @i + 1;
    END;
    SELECT @total AS total;
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

	res, err := executor.Run(prog, "TestTableVarTightLoop", nil, session)
	if err != nil {
		t.Fatalf("executor.Run failed: %v", err)
	}

	// 0 + 1 + ... + 199 = 199 * 200 / 2 = 19900
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["total"]) != "19900" {
		t.Fatalf("expected total 19900, got %v", res.Rows[0]["total"])
	}
}

func TestRuntime_ProcTempTableDDL(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)
	executor.SetTableManager(eng)

	sql := `
CREATE PROCEDURE TestTempTableDDLProc
AS
BEGIN
    CREATE TABLE #temp_orders (id INT PRIMARY KEY, item VARCHAR(50), qty INT);
    CREATE INDEX idx_temp_item ON #temp_orders (item);
    INSERT INTO #temp_orders VALUES (1, 'Keyboard', 2), (2, 'Mouse', 5);
    UPDATE #temp_orders SET qty = 10 WHERE id = 1;
    SELECT id, item, qty FROM #temp_orders WHERE id = 1;
    DROP INDEX idx_temp_item ON #temp_orders;
    DROP TABLE #temp_orders;
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

	res, err := executor.Run(prog, "TestTempTableDDLProc", nil, session)
	if err != nil {
		t.Fatalf("executor.Run failed: %v", err)
	}

	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if fmt.Sprintf("%v", res.Rows[0]["item"]) != "Keyboard" || fmt.Sprintf("%v", res.Rows[0]["qty"]) != "10" {
		t.Fatalf("unexpected row: %v", res.Rows[0])
	}
}

func TestRuntime_TableVariables_InsertOutputInto(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "t_output",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true, Identity: true},
			{Name: "name", Type: storage.TypeVarChar, Nullable: true},
			{Name: "status", Type: storage.TypeVarChar, Nullable: true},
		},
	}); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)
	executor.SetTableManager(eng)

	sql := `
CREATE PROCEDURE sp_TestInsertOutput AS
BEGIN
    DECLARE @inserted_ids TABLE (
        new_id INT,
        inserted_name VARCHAR(20)
    );

    INSERT INTO t_output (name, status)
    OUTPUT INSERTED.id, INSERTED.name INTO @inserted_ids
    VALUES ('Item1', 'ACTIVE'), ('Item2', 'PENDING');

    SELECT new_id, inserted_name FROM @inserted_ids ORDER BY new_id ASC;
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

	res, err := executor.Run(prog, "sp_TestInsertOutput", nil, session)
	if err != nil {
		t.Fatalf("executor.Run failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows in @inserted_ids, got %d: %+v", len(res.Rows), res.Rows)
	}
	if fmt.Sprintf("%v", res.Rows[0]["inserted_name"]) != "Item1" {
		t.Fatalf("row 0 mismatch: %+v", res.Rows[0])
	}
	if fmt.Sprintf("%v", res.Rows[1]["inserted_name"]) != "Item2" {
		t.Fatalf("row 1 mismatch: %+v", res.Rows[1])
	}
}

func TestRuntime_TableVariables_UpdateAndDeleteOutputInto(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	if err := eng.CreateTable(&storage.TableSchema{
		Name: "t_upd_del",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true, Identity: true},
			{Name: "title", Type: storage.TypeVarChar, Nullable: true},
			{Name: "state", Type: storage.TypeVarChar, Nullable: true},
		},
	}); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)
	executor.SetTableManager(eng)

	sql := `
CREATE PROCEDURE sp_TestUpdDelOutput AS
BEGIN
    DECLARE @updated_rows TABLE (
        old_state VARCHAR(20),
        new_state VARCHAR(20)
    );
    DECLARE @deleted_rows TABLE (
        del_id INT,
        del_title VARCHAR(20)
    );

    INSERT INTO t_upd_del (title, state) VALUES ('TaskA', 'OPEN'), ('TaskB', 'OPEN');

    UPDATE t_upd_del
    SET state = 'RESOLVED'
    OUTPUT DELETED.state, INSERTED.state INTO @updated_rows (old_state, new_state)
    WHERE title = 'TaskA';

    DELETE FROM t_upd_del
    OUTPUT DELETED.id, DELETED.title INTO @deleted_rows
    WHERE title = 'TaskB';

    SELECT old_state, new_state FROM @updated_rows;
    SELECT del_id, del_title FROM @deleted_rows;
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

	res, err := executor.Run(prog, "sp_TestUpdDelOutput", nil, session)
	if err != nil {
		t.Fatalf("executor.Run failed: %v", err)
	}

	if len(res.ResultSets) != 2 {
		t.Fatalf("expected 2 result sets, got %d", len(res.ResultSets))
	}
	updRows := res.ResultSets[0].Rows
	delRows := res.ResultSets[1].Rows
	if len(updRows) != 1 || fmt.Sprintf("%v", updRows[0]["old_state"]) != "OPEN" || fmt.Sprintf("%v", updRows[0]["new_state"]) != "RESOLVED" {
		t.Fatalf("unexpected updated row: %+v", updRows)
	}
	if len(delRows) != 1 || fmt.Sprintf("%v", delRows[0]["del_title"]) != "TaskB" {
		t.Fatalf("unexpected deleted row: %+v", delRows)
	}
}

