package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"lionsql/pkg/proc"
	"lionsql/pkg/proc/runtime"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func setupTestDB(t *testing.T) storage.Engine {
	eng := memengine.New()
	err := eng.CreateTable(&storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeInt},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}

	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(1),
		"holder":  storage.StringValue("Alice"),
		"balance": storage.IntValue(1000),
	})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(2),
		"holder":  storage.StringValue("Bob"),
		"balance": storage.IntValue(500),
	})
	_ = tx.Insert("accounts", storage.Record{
		"id":      storage.IntValue(3),
		"holder":  storage.StringValue("Charlie"),
		"balance": storage.IntValue(200),
	})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Seed commit failed: %v", err)
	}

	return eng
}

func TestVM_BankingTransferSuccessAndRollback(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tsqlCode := `
CREATE PROCEDURE TransferFunds
    @from_id INT,
    @to_id INT,
    @amount INT
AS
BEGIN
    PRINT 'Starting transfer';
    DECLARE @fromBal INT;
    SELECT balance INTO @fromBal FROM accounts WHERE id = @from_id;
    IF @fromBal < @amount
    BEGIN
        THROW 'Insufficient funds';
    END
    UPDATE accounts SET balance = balance - @amount WHERE id = @from_id;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to_id;
    PRINT 'Transfer complete';
    RETURN 'OK';
END;
`
	prog, err := proc.Compile(tsqlCode)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName("TransferFunds", prog)
	executor := runtime.NewExecutor(cache)

	// 1. Success case: Transfer $300 from Alice (1) to Bob (2)
	res, err := executor.Run(prog, "TransferFunds", map[string]any{
		"from_id": 1,
		"to_id":   2,
		"amount":  300,
	}, session)
	if err != nil {
		t.Fatalf("TransferFunds failed: %v", err)
	}

	if res.ReturnValue != "OK" {
		t.Errorf("expected return value 'OK', got %v", res.ReturnValue)
	}
	if len(res.Logs) != 2 {
		t.Errorf("expected 2 log entries, got %d: %v", len(res.Logs), res.Logs)
	}

	// Verify committed balances: Alice=700, Bob=800
	txCheck, _ := session.BeginTx(storage.TxOptions{ReadOnly: true})
	aliceRec, _ := txCheck.Get("accounts", storage.IntValue(1))
	bobRec, _ := txCheck.Get("accounts", storage.IntValue(2))
	_ = txCheck.Rollback()

	if aliceRec["balance"].IVal != 700 {
		t.Errorf("expected Alice balance 700, got %d", aliceRec["balance"].IVal)
	}
	if bobRec["balance"].IVal != 800 {
		t.Errorf("expected Bob balance 800, got %d", bobRec["balance"].IVal)
	}

	// 2. Overdraft failure case: Try transferring $5000 from Alice (1)
	_, err = executor.Run(prog, "TransferFunds", map[string]any{
		"from_id": 1,
		"to_id":   2,
		"amount":  5000,
	}, session)
	if err == nil {
		t.Fatalf("expected overdraft error, but execution succeeded")
	}

	// Verify rollback: Alice should still have 700, Bob should still have 800
	txCheck2, _ := session.BeginTx(storage.TxOptions{ReadOnly: true})
	aliceRec2, _ := txCheck2.Get("accounts", storage.IntValue(1))
	bobRec2, _ := txCheck2.Get("accounts", storage.IntValue(2))
	_ = txCheck2.Rollback()

	if aliceRec2["balance"].IVal != 700 {
		t.Errorf("expected Alice balance unchanged at 700 after rollback, got %d", aliceRec2["balance"].IVal)
	}
	if bobRec2["balance"].IVal != 800 {
		t.Errorf("expected Bob balance unchanged at 800 after rollback, got %d", bobRec2["balance"].IVal)
	}
}

func TestVM_ConcurrentExecutionRaceFree(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	tsqlCode := `
CREATE PROCEDURE RegisterUser
    @id INT,
    @name VARCHAR(50),
    @initial_deposit INT
AS
BEGIN
    INSERT INTO accounts (id, holder, balance) VALUES (@id, @name, @initial_deposit);
    DECLARE @bal INT;
    SELECT balance INTO @bal FROM accounts WHERE id = @id;
    RETURN @bal;
END;
`
	prog, err := proc.Compile(tsqlCode)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName("RegisterUser", prog)
	executor := runtime.NewExecutor(cache)

	var wg sync.WaitGroup
	workers := 10

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			session, err := eng.NewSession()
			if err != nil {
				t.Errorf("NewSession failed: %v", err)
				return
			}
			defer session.Close()

			accID := int64(10 + workerID)
			res, err := executor.Run(prog, "RegisterUser", map[string]any{
				"id":              accID,
				"name":            "User",
				"initial_deposit": 500 + workerID*10,
			}, session)
			if err != nil {
				t.Errorf("worker %d failed: %v", workerID, err)
				return
			}
			if res.ReturnValue == nil {
				t.Errorf("worker %d expected return value, got nil", workerID)
			}
		}(w)
	}

	wg.Wait()

	session, _ := eng.NewSession()
	defer session.Close()
	tx, _ := session.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx.Rollback()

	// Verify all 10 concurrent inserts were committed successfully
	for w := 0; w < workers; w++ {
		accID := int64(10 + w)
		rec, err := tx.Get("accounts", storage.IntValue(accID))
		if err != nil {
			t.Errorf("expected account %d to exist: %v", accID, err)
			continue
		}
		expectedDeposit := int64(500 + w*10)
		if rec["balance"].IVal != expectedDeposit {
			t.Errorf("account %d: expected balance %d, got %d", accID, expectedDeposit, rec["balance"].IVal)
		}
	}
}

func TestVM_EndToEndTSqlTryCatchAndCase(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tsqlScript := `
CREATE PROCEDURE ProcessBonus
    @id INT,
    @tier VARCHAR(10)
AS
BEGIN
    DECLARE @bonus_amount INT;
    DECLARE @clean_tier VARCHAR(10);
    SET @clean_tier = ISNULL(@tier, 'STANDARD');
    SET @bonus_amount = CASE 
        WHEN @clean_tier = 'VIP' THEN 500
        WHEN @clean_tier = 'GOLD' THEN 200
        ELSE 50
    END;

    UPDATE accounts SET balance = balance + @bonus_amount WHERE id = @id;
    PRINT 'Bonus applied successfully';
    RETURN @bonus_amount;
END;
`
	prog, err := proc.Compile(tsqlScript)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName("ProcessBonus", prog)
	executor := runtime.NewExecutor(cache)

	res, err := executor.Run(prog, "ProcessBonus", map[string]any{
		"id":   1,
		"tier": "VIP",
	}, session)
	if err != nil {
		t.Fatalf("ProcessBonus failed: %v", err)
	}

	if fmt.Sprintf("%v", res.ReturnValue) != "500" {
		t.Errorf("expected return value 500, got %v", res.ReturnValue)
	}

	txCheck, _ := session.BeginTx(storage.TxOptions{ReadOnly: true})
	alice, _ := txCheck.Get("accounts", storage.IntValue(1))
	_ = txCheck.Rollback()

	if alice["balance"].IVal != 1500 {
		t.Errorf("expected Alice balance 1500, got %d", alice["balance"].IVal)
	}
}

func TestVM_CursorIterationAndBatchProcessing(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tsqlScript := `
CREATE PROCEDURE ApplyInterest
    @rate FLOAT
AS
BEGIN
    DECLARE @id INT;
    DECLARE @bal INT;
    DECLARE @total_awarded INT;
    SET @total_awarded = 0;

    DECLARE acc_cur CURSOR FOR SELECT id, balance FROM accounts WHERE balance > 0;
    OPEN acc_cur;
    FETCH NEXT FROM acc_cur INTO @id, @bal;

    WHILE @@FETCH_STATUS = 0
    BEGIN
        DECLARE @interest INT;
        SET @interest = @bal * 0.1;
        UPDATE accounts SET balance = balance + @interest WHERE id = @id;
        SET @total_awarded = @total_awarded + @interest;
        FETCH NEXT FROM acc_cur INTO @id, @bal;
    END

    CLOSE acc_cur;
    DEALLOCATE acc_cur;
    RETURN @total_awarded;
END;
`
	prog, err := proc.Compile(tsqlScript)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName("ApplyInterest", prog)
	executor := runtime.NewExecutor(cache)

	res, err := executor.Run(prog, "ApplyInterest", map[string]any{
		"rate": 0.1,
	}, session)
	if err != nil {
		t.Fatalf("ApplyInterest failed: %v", err)
	}

	if fmt.Sprintf("%v", res.ReturnValue) != "170" {
		t.Errorf("expected total awarded 170, got %v", res.ReturnValue)
	}

	txCheck, _ := session.BeginTx(storage.TxOptions{ReadOnly: true})
	alice, _ := txCheck.Get("accounts", storage.IntValue(1))
	bob, _ := txCheck.Get("accounts", storage.IntValue(2))
	charlie, _ := txCheck.Get("accounts", storage.IntValue(3))
	_ = txCheck.Rollback()

	if alice["balance"].IVal != 1100 {
		t.Errorf("expected Alice 1100, got %d", alice["balance"].IVal)
	}
	if bob["balance"].IVal != 550 {
		t.Errorf("expected Bob 550, got %d", bob["balance"].IVal)
	}
	if charlie["balance"].IVal != 220 {
		t.Errorf("expected Charlie 220, got %d", charlie["balance"].IVal)
	}
}

func TestVM_MultiRowUpdateAndDeleteWhere(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tsqlScript := `
CREATE PROCEDURE MassRebalance
    @threshold INT
AS
BEGIN
    UPDATE accounts SET balance = balance + 100 WHERE balance < @threshold;
    DELETE FROM accounts WHERE balance <= 300;
    RETURN 0;
END;
`
	prog, err := proc.Compile(tsqlScript)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName("MassRebalance", prog)
	executor := runtime.NewExecutor(cache)

	_, err = executor.Run(prog, "MassRebalance", map[string]any{
		"threshold": 600,
	}, session)
	if err != nil {
		t.Fatalf("MassRebalance failed: %v", err)
	}

	txCheck, _ := session.BeginTx(storage.TxOptions{ReadOnly: true})
	alice, _ := txCheck.Get("accounts", storage.IntValue(1))
	bob, _ := txCheck.Get("accounts", storage.IntValue(2))
	_, charlieErr := txCheck.Get("accounts", storage.IntValue(3))
	_ = txCheck.Rollback()

	if alice["balance"].IVal != 1000 {
		t.Errorf("expected Alice 1000, got %d", alice["balance"].IVal)
	}
	if bob["balance"].IVal != 600 {
		t.Errorf("expected Bob 600, got %d", bob["balance"].IVal)
	}
	if !errors.Is(charlieErr, storage.ErrKeyNotFound) {
		t.Errorf("expected Charlie to be deleted, got error: %v", charlieErr)
	}
}

func TestVM_LikeInBetweenOperators(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tsqlScript := `
CREATE PROCEDURE ValidateUser
    @name VARCHAR(50),
    @val INT
AS
BEGIN
    IF @name LIKE 'Ali%' AND @val BETWEEN 500 AND 1500
    BEGIN
        RETURN 1;
    END
    RETURN 0;
END;
`
	prog, err := proc.Compile(tsqlScript)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	cache.PutByName("ValidateUser", prog)
	executor := runtime.NewExecutor(cache)

	res1, _ := executor.Run(prog, "ValidateUser", map[string]any{"name": "Alice", "val": 1000}, session)
	if fmt.Sprintf("%v", res1.ReturnValue) != "1" {
		t.Errorf("expected 1, got %v", res1.ReturnValue)
	}

	res2, _ := executor.Run(prog, "ValidateUser", map[string]any{"name": "Bob", "val": 1000}, session)
	if fmt.Sprintf("%v", res2.ReturnValue) != "0" {
		t.Errorf("expected 0, got %v", res2.ReturnValue)
	}
}

func TestVM_OpRelQueryVolcanoExecution(t *testing.T) {
	eng := setupTestDB(t)
	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	query := `SELECT id, holder, balance FROM accounts WHERE balance >= 500 ORDER BY balance DESC`
	prog, err := proc.Compile(query)
	if err != nil {
		t.Fatalf("Compile query failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	executor := runtime.NewExecutor(cache)
	executor.SetTableGetter(eng.GetTable)

	res, err := executor.Run(prog, "Query", nil, session)
	if err != nil {
		t.Fatalf("executor.Run failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(res.Rows), res.Rows)
	}

	first := res.Rows[0]
	if fmt.Sprintf("%v", first["holder"]) != "Alice" || fmt.Sprintf("%v", first["balance"]) != "1000" {
		t.Errorf("expected Alice 1000 as first row, got %v", first)
	}

	second := res.Rows[1]
	if fmt.Sprintf("%v", second["holder"]) != "Bob" || fmt.Sprintf("%v", second["balance"]) != "500" {
		t.Errorf("expected Bob 500 as second row, got %v", second)
	}

	// Verify plan caching on repeat execution
	res2, err := executor.Run(prog, "Query", nil, session)
	if err != nil {
		t.Fatalf("second executor.Run failed: %v", err)
	}
	if len(res2.Rows) != 2 {
		t.Fatalf("expected 2 rows on cached run, got %d", len(res2.Rows))
	}

	foundCache := false
	for _, inst := range prog.Instructions {
		if inst.Op == proc.OpRelQuery {
			if qData, ok := inst.GetData(prog).(*proc.RelQueryData); ok && qData.GetCachedQueryInfo() != nil {
				foundCache = true
				break
			}
		}
	}
	if !foundCache {
		t.Errorf("expected OpRelQuery to have CachedQueryInfo populated after run")
	}
}

func TestVM_OpRelQueryWithJoin(t *testing.T) {
	eng := setupTestDB(t)
	err := eng.CreateTable(&storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "account_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable orders failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, _ := session.BeginTx(storage.TxOptions{})
	_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(101), "account_id": storage.IntValue(1), "amount": storage.FloatValue(99.9)})
	_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(102), "account_id": storage.IntValue(2), "amount": storage.FloatValue(149.5)})
	_ = tx.Commit()

	query := `SELECT accounts.holder, orders.amount FROM accounts JOIN orders ON accounts.id = orders.account_id ORDER BY orders.amount DESC`
	prog, err := proc.Compile(query)
	if err != nil {
		t.Fatalf("Compile join query failed: %v", err)
	}

	executor := runtime.NewExecutor(runtime.NewPlanCache())
	executor.SetTableGetter(eng.GetTable)

	res, err := executor.Run(prog, "JoinQuery", nil, session)
	if err != nil {
		t.Fatalf("executor.Run join query failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 joined rows, got %d: %v", len(res.Rows), res.Rows)
	}
}

func TestVM_OpRelQueryMultiTableChainedJoins(t *testing.T) {
	eng := setupTestDB(t)
	err := eng.CreateTable(&storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "account_id", Type: storage.TypeInt},
			{Name: "amount", Type: storage.TypeFloat},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable orders failed: %v", err)
	}

	err = eng.CreateTable(&storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "order_id", Type: storage.TypeInt},
			{Name: "product", Type: storage.TypeVarChar},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable items failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, _ := session.BeginTx(storage.TxOptions{})
	_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(101), "account_id": storage.IntValue(1), "amount": storage.FloatValue(99.9)})
	_ = tx.Insert("orders", storage.Record{"id": storage.IntValue(102), "account_id": storage.IntValue(2), "amount": storage.FloatValue(149.5)})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1001), "order_id": storage.IntValue(101), "product": storage.StringValue("Keyboard")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1002), "order_id": storage.IntValue(102), "product": storage.StringValue("Monitor")})
	_ = tx.Commit()

	query := `SELECT accounts.holder, orders.amount, items.product FROM accounts JOIN orders ON accounts.id = orders.account_id JOIN items ON orders.id = items.order_id ORDER BY items.product ASC`
	prog, err := proc.Compile(query)
	if err != nil {
		t.Fatalf("Compile multi-join query failed: %v", err)
	}

	executor := runtime.NewExecutor(runtime.NewPlanCache())
	executor.SetTableGetter(eng.GetTable)

	res, err := executor.Run(prog, "MultiJoinQuery", nil, session)
	if err != nil {
		t.Fatalf("executor.Run multi-join query failed: %v", err)
	}

	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 joined rows, got %d: %v", len(res.Rows), res.Rows)
	}

	if fmt.Sprintf("%v", res.Rows[0]["product"]) != "Keyboard" || fmt.Sprintf("%v", res.Rows[0]["holder"]) != "Alice" {
		t.Errorf("unexpected first row: %v", res.Rows[0])
	}
	if fmt.Sprintf("%v", res.Rows[1]["product"]) != "Monitor" || fmt.Sprintf("%v", res.Rows[1]["holder"]) != "Bob" {
		t.Errorf("unexpected second row: %v", res.Rows[1])
	}
}

func TestVM_OpIndexScan(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	err := eng.CreateTable(&storage.TableSchema{
		Name: "employees",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "dept", Type: storage.TypeVarChar},
			{Name: "salary", Type: storage.TypeInt},
		},
	})
	if err != nil {
		t.Fatalf("CreateTable employees failed: %v", err)
	}

	err = eng.CreateIndex(&storage.IndexSchema{
		Name:      "idx_emp_dept",
		TableName: "employees",
		Columns:   []string{"dept"},
	})
	if err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	tx, _ := session.BeginTx(storage.TxOptions{})
	_ = tx.Insert("employees", storage.Record{"id": storage.IntValue(1), "dept": storage.StringValue("HR"), "salary": storage.IntValue(4000)})
	_ = tx.Insert("employees", storage.Record{"id": storage.IntValue(2), "dept": storage.StringValue("IT"), "salary": storage.IntValue(7000)})
	_ = tx.Insert("employees", storage.Record{"id": storage.IntValue(3), "dept": storage.StringValue("IT"), "salary": storage.IntValue(8500)})
	_ = tx.Insert("employees", storage.Record{"id": storage.IntValue(4), "dept": storage.StringValue("MKT"), "salary": storage.IntValue(5000)})
	_ = tx.Commit()

	executor := runtime.NewExecutor(runtime.NewPlanCache())
	executor.SetTableGetter(eng.GetTable)

	// 1. Test direct OpIndexScan execution
	prog := &proc.Program{
		Name:         "TestIndexScan",
		Type:         "QUERY",
		NumRegisters: 4,
	}
	prog.AddInstruction(proc.OpIndexScan, 0, 0, 0, &proc.IndexScanData{
		Table:       "employees",
		IndexName:   "idx_emp_dept",
		LowVal:      &proc.LiteralExpr{Val: storage.StringValue("IT")},
		HighVal:     &proc.LiteralExpr{Val: storage.StringValue("IT")},
		Projections: []string{"id", "dept", "salary"},
	})
	prog.AddInstruction(proc.OpReturn, -1, 0, 0, nil)

	res, err := executor.Run(prog, "TestIndexScan", nil, session)
	if err != nil {
		t.Fatalf("OpIndexScan failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 IT rows from OpIndexScan, got %d: %+v", len(res.Rows), res.Rows)
	}
	for _, r := range res.Rows {
		if fmt.Sprintf("%v", r["dept"]) != "IT" {
			t.Errorf("expected dept IT, got %v", r["dept"])
		}
	}

	// 2. Test OpUpdateWhere utilizing index scan
	updateProg := &proc.Program{
		Name:         "TestIndexUpdate",
		Type:         "PROCEDURE",
		NumRegisters: 4,
	}
	updateProg.AddInstruction(proc.OpUpdateWhere, 0, 0, 0, &proc.DMLData{
		Table: "employees",
		Assignments: []proc.UpdateAssignment{
			{Column: "salary", Value: &proc.LiteralExpr{Val: storage.IntValue(9999)}},
		},
		WhereExpr: &proc.BinaryExpr{
			Left:  &proc.ColumnRefExpr{Name: "dept"},
			Op:    proc.TokenEq,
			Right: &proc.LiteralExpr{Val: storage.StringValue("IT")},
		},
	})
	updateProg.AddInstruction(proc.OpReturn, -1, 0, 0, nil)

	updateRes, err := executor.Run(updateProg, "TestIndexUpdate", nil, session)
	if err != nil {
		t.Fatalf("OpUpdateWhere with index failed: %v", err)
	}
	if updateRes.RowsAffected != 2 {
		t.Fatalf("expected 2 rows updated, got %d", updateRes.RowsAffected)
	}

	// 3. Test OpDeleteWhere utilizing index scan
	deleteProg := &proc.Program{
		Name:         "TestIndexDelete",
		Type:         "PROCEDURE",
		NumRegisters: 4,
	}
	deleteProg.AddInstruction(proc.OpDeleteWhere, 0, 0, 0, &proc.DMLData{
		Table: "employees",
		WhereExpr: &proc.BinaryExpr{
			Left:  &proc.ColumnRefExpr{Name: "dept"},
			Op:    proc.TokenEq,
			Right: &proc.LiteralExpr{Val: storage.StringValue("IT")},
		},
	})
	deleteProg.AddInstruction(proc.OpReturn, -1, 0, 0, nil)

	deleteRes, err := executor.Run(deleteProg, "TestIndexDelete", nil, session)
	if err != nil {
		t.Fatalf("OpDeleteWhere with index failed: %v", err)
	}
	if deleteRes.RowsAffected != 2 {
		t.Fatalf("expected 2 rows deleted, got %d", deleteRes.RowsAffected)
	}
}

func TestRunContext_Cancellation(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	session, _ := eng.NewSession()
	defer session.Close()

	executor := runtime.NewExecutor(runtime.NewPlanCache())

	// Infinite loop bytecode: OpJump 0
	loopProg := &proc.Program{
		Name: "TestInfiniteLoop",
		Type: "PROCEDURE",
		Instructions: []proc.Instruction{
			{Op: proc.OpJump, A: 0},
		},
		NumRegisters: 1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := executor.RunContext(ctx, loopProg, "TestInfiniteLoop", nil, session)
	if err == nil {
		t.Fatal("expected context deadline exceeded or cancellation error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetOptionStmt(t *testing.T) {
	script := `
CREATE PROCEDURE TestProcWithSetOptions AS
BEGIN
	SET NOCOUNT ON;
	SET ANSI_NULLS ON, QUOTED_IDENTIFIER ON;
	RETURN 42;
END;
`
	prog, err := proc.Compile(script)
	if err != nil {
		t.Fatalf("failed to compile script with SET options: %v", err)
	}

	eng := memengine.New()
	defer eng.Close()
	session, _ := eng.NewSession()
	defer session.Close()

	executor := runtime.NewExecutor(runtime.NewPlanCache())
	res, err := executor.Run(prog, "TestProcWithSetOptions", nil, session)
	if err != nil {
		t.Fatalf("failed to run procedure with SET options: %v", err)
	}
	if fmt.Sprintf("%v", res.ReturnValue) != "42" {
		t.Fatalf("expected return 42, got %v", res.ReturnValue)
	}
}



