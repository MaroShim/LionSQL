package engine_test

import (
	"context"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func setupTestCoordinatorWithSession(t *testing.T) (*engine.Coordinator, string) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	sessID := "test-session-assign"
	return coord, sessID
}

// TestSelectAssign_Tableless verifies table-less variable assignment: SELECT @x = 100, @y = 200
func TestSelectAssign_Tableless(t *testing.T) {
	coord, sessID := setupTestCoordinatorWithSession(t)
	defer coord.Close()
	ctx := context.Background()

	// 1. Declare and assign table-less
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @x = 100, @y = 200")
	if err != nil {
		t.Fatalf("unexpected error on table-less SELECT assign: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.Error)
	}
	if len(res.Rows) != 0 || len(res.Columns) != 0 {
		t.Fatalf("expected 0 rows and columns from SELECT assign, got %d rows, %d cols", len(res.Rows), len(res.Columns))
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected RowsAffected = 1, got %d", res.RowsAffected)
	}

	// 2. Query variables back and verify values
	resX, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @x AS x")
	if err != nil || len(resX.Rows) == 0 {
		t.Fatalf("failed to query @x: %v", err)
	}
	if fmtVal := resX.Rows[0]["x"]; fmtVal != int64(100) && fmtVal != 100 {
		t.Fatalf("expected @x = 100, got %v (%T)", fmtVal, fmtVal)
	}

	resY, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @y AS y")
	if err != nil || len(resY.Rows) == 0 {
		t.Fatalf("failed to query @y: %v", err)
	}
	if fmtVal := resY.Rows[0]["y"]; fmtVal != int64(200) && fmtVal != 200 {
		t.Fatalf("expected @y = 200, got %v (%T)", fmtVal, fmtVal)
	}

	// 3. Test sequential calculation: @z = @x + @y
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @z = @x + @y")
	if err != nil {
		t.Fatalf("unexpected error on @z = @x + @y: %v", err)
	}
	resZ, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @z AS z")
	if err != nil || len(resZ.Rows) == 0 {
		t.Fatalf("failed to query @z: %v", err)
	}
	if fmtVal := resZ.Rows[0]["z"]; fmtVal != int64(300) && fmtVal != 300 {
		t.Fatalf("expected @z = 300, got %v (%T)", fmtVal, fmtVal)
	}
}

// TestSelectAssign_NoMixingRule verifies Msg 141 error when mixing assignment and data retrieval
func TestSelectAssign_NoMixingRule(t *testing.T) {
	coord, sessID := setupTestCoordinatorWithSession(t)
	defer coord.Close()
	ctx := context.Background()

	// 1. Mixing @var = expr and regular expression without FROM
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @x = 1, 2")
	if err == nil {
		t.Fatalf("expected Msg 141 error when mixing assignment and scalar data retrieval, got nil")
	}
	if !strings.Contains(err.Error(), "Msg 141") {
		t.Fatalf("expected error containing Msg 141, got: %v", err)
	}

	// 2. Mixing @var = expr and column alias (y = 2)
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @x = 1, y = 2")
	if err == nil {
		t.Fatalf("expected Msg 141 error when mixing assignment and column alias, got nil")
	}
	if !strings.Contains(err.Error(), "Msg 141") {
		t.Fatalf("expected error containing Msg 141, got: %v", err)
	}
}

// TestSelectAssign_WithFromClause verifies SELECT @var = col FROM table WHERE ...
func TestSelectAssign_WithFromClause(t *testing.T) {
	coord, sessID := setupTestCoordinatorWithSession(t)
	defer coord.Close()
	ctx := context.Background()

	// 1. Prepare table
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE products (id INT PRIMARY KEY, name VARCHAR(50), price INT)")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO products VALUES (1, 'Apple', 100), (2, 'Banana', 200), (3, 'Cherry', 300)")
	if err != nil {
		t.Fatalf("failed to insert data: %v", err)
	}

	// 2. Single-row assignment from table
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @p = price, @n = name FROM products WHERE id = 2")
	if err != nil {
		t.Fatalf("unexpected error on SELECT assign with FROM: %v", err)
	}
	if len(res.Rows) != 0 || len(res.Columns) != 0 {
		t.Fatalf("expected 0 rows/cols returned, got %d rows, %d cols", len(res.Rows), len(res.Columns))
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected RowsAffected = 1, got %d", res.RowsAffected)
	}

	resP, _ := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @p AS p, @n AS n")
	if len(resP.Rows) == 0 || resP.Rows[0]["p"] != int64(200) || resP.Rows[0]["n"] != "Banana" {
		t.Fatalf("expected @p=200, @n='Banana', got: %v", resP.Rows)
	}

	// 3. Multi-row matching: last row wins
	res, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @p = price FROM products ORDER BY id ASC")
	if err != nil {
		t.Fatalf("unexpected error on multi-row assign: %v", err)
	}
	if res.RowsAffected != 3 {
		t.Fatalf("expected RowsAffected = 3, got %d", res.RowsAffected)
	}
	resP, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @p AS p")
	if resP.Rows[0]["p"] != int64(300) {
		t.Fatalf("expected last row price 300, got: %v", resP.Rows[0]["p"])
	}

	// 4. Zero-row matching: variable retains previous value (does NOT become NULL)
	res, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @p = price FROM products WHERE 1 = 0")
	if err != nil {
		t.Fatalf("unexpected error on zero-row assign: %v", err)
	}
	if res.RowsAffected != 0 {
		t.Fatalf("expected RowsAffected = 0, got %d", res.RowsAffected)
	}
	resP, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @p AS p")
	if resP.Rows[0]["p"] != int64(300) {
		t.Fatalf("expected @p to retain previous value 300, got: %v", resP.Rows[0]["p"])
	}

	// 5. Aggregate assignment: SELECT @cnt = COUNT(*) FROM products
	res, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @cnt = COUNT(*) FROM products")
	if err != nil {
		t.Fatalf("unexpected error on aggregate assign: %v", err)
	}
	resCnt, _ := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @cnt AS cnt")
	if resCnt.Rows[0]["cnt"] != int64(3) && resCnt.Rows[0]["cnt"] != 3 {
		t.Fatalf("expected @cnt = 3, got: %v", resCnt.Rows[0]["cnt"])
	}
}

// TestSelectAssign_RunningTotal verifies T-SQL running accumulation across rows
func TestSelectAssign_RunningTotal(t *testing.T) {
	coord, sessID := setupTestCoordinatorWithSession(t)
	defer coord.Close()
	ctx := context.Background()

	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "CREATE TABLE ledger (id INT PRIMARY KEY, amount INT)")
	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "INSERT INTO ledger VALUES (1, 10), (2, 20), (3, 30)")

	// Initialize @tot = 0
	_, _ = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @tot = 0")

	// Accumulate: SELECT @tot = @tot + amount FROM ledger ORDER BY id ASC
	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @tot = @tot + amount FROM ledger ORDER BY id ASC")
	if err != nil {
		t.Fatalf("unexpected error on running total assign: %v", err)
	}
	if res.RowsAffected != 3 {
		t.Fatalf("expected RowsAffected = 3, got %d", res.RowsAffected)
	}

	resTot, _ := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @tot AS tot")
	if resTot.Rows[0]["tot"] != int64(60) && resTot.Rows[0]["tot"] != 60 {
		t.Fatalf("expected @tot = 60 (10+20+30), got: %v", resTot.Rows[0]["tot"])
	}
}

// TestSelectAssign_InProcedure verifies SELECT @var = expr inside stored procedures and sp_executesql
func TestSelectAssign_InProcedure(t *testing.T) {
	coord, sessID := setupTestCoordinatorWithSession(t)
	defer coord.Close()
	ctx := context.Background()

	// 1. Stored Procedure with table-less SELECT assign
	procSQL := `
CREATE PROCEDURE dbo.sp_calc_tax
AS
BEGIN
    DECLARE @rate INT = 10;
    DECLARE @val INT = 500;
    DECLARE @tax INT;
    SELECT @tax = @val * @rate / 100;
    SELECT @tax AS tax;
END;
`
	_, err := coord.ExecuteSessionSQLContext(ctx, sessID, procSQL)
	if err != nil {
		t.Fatalf("failed to create procedure: %v", err)
	}

	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "EXEC dbo.sp_calc_tax")
	if err != nil {
		t.Fatalf("failed to execute procedure: %v", err)
	}
	if len(res.Rows) != 1 || (res.Rows[0]["tax"] != int64(50) && res.Rows[0]["tax"] != 50) {
		t.Fatalf("expected procedure output tax = 50, got: %v", res.Rows)
	}

	// 2. sp_executesql anonymous block with SELECT assign
	dynSQL := `EXEC sp_executesql N'DECLARE @num INT; SELECT @num = 777; SELECT @num AS result;'`
	resDyn, err := coord.ExecuteSessionSQLContext(ctx, sessID, dynSQL)
	if err != nil {
		t.Fatalf("failed to execute sp_executesql: %v", err)
	}
	if len(resDyn.Rows) != 1 || (resDyn.Rows[0]["result"] != int64(777) && resDyn.Rows[0]["result"] != 777) {
		t.Fatalf("expected sp_executesql output result = 777, got: %v", resDyn.Rows)
	}
}
