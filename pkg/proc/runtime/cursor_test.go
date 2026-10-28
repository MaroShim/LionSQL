package runtime_test

import (
	"fmt"
	"testing"

	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func setupCursorTestDB(t *testing.T) storage.Engine {
	eng := memengine.New()
	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeFloat},
			{Name: "status", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	session, _ := eng.NewSession()
	defer session.Close()
	tx, _ := session.BeginTx(storage.TxOptions{})
	defer tx.Rollback()

	data := []struct {
		id     int64
		holder string
		bal    float64
		status string
	}{
		{1, "Alice", 1000.0, "ACTIVE"},
		{2, "Bob", 500.0, "ACTIVE"},
		{3, "Charlie", 200.0, "SUSPENDED"},
		{4, "Dave", 1500.0, "ACTIVE"},
		{5, "Eve", 800.0, "SUSPENDED"},
	}

	for _, d := range data {
		rec := storage.Record{
			"id":      storage.IntValue(d.id),
			"holder":  storage.StringValue(d.holder),
			"balance": storage.FloatValue(d.bal),
			"status":  storage.StringValue(d.status),
		}
		if err := tx.Insert("accounts", rec); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}
	_ = tx.Commit()
	return eng
}

func runCursorProc(t *testing.T, eng storage.Engine, script, procName string, params map[string]any) (*runtime.ExecutionResult, error) {
	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(func(name string) (*storage.TableSchema, error) {
		return eng.GetTable(name)
	})

	prog, err := cache.GetOrCompile(script)
	if err != nil {
		return nil, fmt.Errorf("compile error: %w", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		return nil, fmt.Errorf("session error: %w", err)
	}
	defer session.Close()

	return executor.Run(prog, procName, params, session)
}

func TestCursor_TolerantModifiersAndOrderBy(t *testing.T) {
	eng := setupCursorTestDB(t)
	defer eng.Close()

	// Tests LOCAL FAST_FORWARD CURSOR with ORDER BY balance DESC
	tsql := `
CREATE PROCEDURE CalculateActiveBalances
AS
BEGIN
    DECLARE @id INT;
    DECLARE @bal FLOAT;
    DECLARE @sum FLOAT;
    DECLARE @firstId INT;
    SET @sum = 0.0;
    SET @firstId = 0;

    DECLARE cur LOCAL FAST_FORWARD CURSOR FOR 
        SELECT id, balance FROM accounts WHERE status = 'ACTIVE' ORDER BY balance DESC;
    OPEN cur;
    FETCH NEXT FROM cur INTO @id, @bal;

    IF @@FETCH_STATUS = 0
    BEGIN
        SET @firstId = @id;
    END

    WHILE @@FETCH_STATUS = 0
    BEGIN
        SET @sum = @sum + @bal;
        FETCH NEXT FROM cur INTO @id, @bal;
    END

    CLOSE cur;
    DEALLOCATE cur;
    RETURN @firstId;
END;
`
	res, err := runCursorProc(t, eng, tsql, "CalculateActiveBalances", nil)
	if err != nil {
		t.Fatalf("CalculateActiveBalances failed: %v", err)
	}

	// First account with highest balance among ACTIVE (Dave: 1500, Alice: 1000, Bob: 500) is Dave (id=4)
	if fmt.Sprintf("%v", res.ReturnValue) != "4" {
		t.Errorf("Expected firstId=4 (Dave), got %v", res.ReturnValue)
	}
	if fmt.Sprintf("%v", res.Vars["sum"]) != "3000" {
		t.Errorf("Expected sum=3000.0, got %v", res.Vars["sum"])
	}
}

func TestCursor_SelectStarExpansion(t *testing.T) {
	eng := setupCursorTestDB(t)
	defer eng.Close()

	// Tests SELECT * expansion with CURSOR FORWARD_ONLY READ_ONLY
	tsql := `
CREATE PROCEDURE IterateAllAccountsStar
AS
BEGIN
    DECLARE @id INT;
    DECLARE @holder VARCHAR(50);
    DECLARE @bal FLOAT;
    DECLARE @status VARCHAR(20);
    DECLARE @count INT;
    SET @count = 0;

    DECLARE cur_star CURSOR FORWARD_ONLY READ_ONLY FOR 
        SELECT * FROM accounts;
    OPEN cur_star;
    FETCH NEXT FROM cur_star INTO @id, @holder, @bal, @status;

    WHILE @@FETCH_STATUS = 0
    BEGIN
        SET @count = @count + 1;
        FETCH NEXT FROM cur_star INTO @id, @holder, @bal, @status;
    END

    CLOSE cur_star;
    DEALLOCATE cur_star;
    RETURN @count;
END;
`
	res, err := runCursorProc(t, eng, tsql, "IterateAllAccountsStar", nil)
	if err != nil {
		t.Fatalf("IterateAllAccountsStar failed: %v", err)
	}

	if fmt.Sprintf("%v", res.ReturnValue) != "5" {
		t.Errorf("Expected 5 total accounts iterated, got %v", res.ReturnValue)
	}
}

func TestCursor_EmptyResultSet(t *testing.T) {
	eng := setupCursorTestDB(t)
	defer eng.Close()

	// Tests empty query where @@FETCH_STATUS should be -1 immediately
	tsql := `
CREATE PROCEDURE IterateNonExistent
AS
BEGIN
    DECLARE @id INT;
    DECLARE @bal FLOAT;
    DECLARE @fetched INT;
    SET @fetched = 0;

    DECLARE empty_cur CURSOR FOR 
        SELECT id, balance FROM accounts WHERE id > 9999;
    OPEN empty_cur;
    FETCH NEXT FROM empty_cur INTO @id, @bal;

    IF @@FETCH_STATUS = -1
    BEGIN
        SET @fetched = 999;
    END

    WHILE @@FETCH_STATUS = 0
    BEGIN
        SET @fetched = @fetched + 1;
        FETCH NEXT FROM empty_cur INTO @id, @bal;
    END

    CLOSE empty_cur;
    DEALLOCATE empty_cur;
    RETURN @fetched;
END;
`
	res, err := runCursorProc(t, eng, tsql, "IterateNonExistent", nil)
	if err != nil {
		t.Fatalf("IterateNonExistent failed: %v", err)
	}

	if fmt.Sprintf("%v", res.ReturnValue) != "999" {
		t.Errorf("Expected return value 999 indicating immediate @@FETCH_STATUS = -1, got %v", res.ReturnValue)
	}
}

func TestCursor_MultipleIndependentCursors(t *testing.T) {
	eng := setupCursorTestDB(t)
	defer eng.Close()

	// Tests two independent cursors in the same procedure
	tsql := `
CREATE PROCEDURE MultiCursorProc
AS
BEGIN
    DECLARE @id1 INT;
    DECLARE @bal1 FLOAT;
    DECLARE @id2 INT;
    DECLARE @bal2 FLOAT;
    DECLARE @activeCount INT;
    DECLARE @suspendedCount INT;
    SET @activeCount = 0;
    SET @suspendedCount = 0;

    DECLARE cur_act CURSOR FOR SELECT id, balance FROM accounts WHERE status = 'ACTIVE';
    DECLARE cur_susp CURSOR FOR SELECT id, balance FROM accounts WHERE status = 'SUSPENDED';

    OPEN cur_act;
    FETCH NEXT FROM cur_act INTO @id1, @bal1;
    WHILE @@FETCH_STATUS = 0
    BEGIN
        SET @activeCount = @activeCount + 1;
        FETCH NEXT FROM cur_act INTO @id1, @bal1;
    END
    CLOSE cur_act;
    DEALLOCATE cur_act;

    OPEN cur_susp;
    FETCH NEXT FROM cur_susp INTO @id2, @bal2;
    WHILE @@FETCH_STATUS = 0
    BEGIN
        SET @suspendedCount = @suspendedCount + 1;
        FETCH NEXT FROM cur_susp INTO @id2, @bal2;
    END
    CLOSE cur_susp;
    DEALLOCATE cur_susp;

    RETURN @activeCount * 10 + @suspendedCount;
END;
`
	res, err := runCursorProc(t, eng, tsql, "MultiCursorProc", nil)
	if err != nil {
		t.Fatalf("MultiCursorProc failed: %v", err)
	}

	// 3 active * 10 + 2 suspended = 32
	if fmt.Sprintf("%v", res.ReturnValue) != "32" {
		t.Errorf("Expected 32 (3 active, 2 suspended), got %v", res.ReturnValue)
	}
}
