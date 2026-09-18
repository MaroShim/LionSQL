package runtime_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"sqlion/pkg/proc"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
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

func TestGoja_BankingTransferSuccessAndRollback(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	jsCode := `
function TransferFunds(params, db) {
	console.log("Starting transfer:", params.from_id, "->", params.to_id, "amount:", params.amount);
	let fromBal = db.get("accounts", params.from_id, "balance");
	if (fromBal < params.amount) {
		throw new Error("Insufficient funds: balance is " + fromBal + ", required " + params.amount);
	}
	db.update("accounts", params.from_id, { balance: fromBal - params.amount });
	let toBal = db.get("accounts", params.to_id, "balance");
	db.update("accounts", params.to_id, { balance: toBal + params.amount });
	console.log("Transfer complete");
	return "OK";
}
`
	cache := runtime.NewPlanCache()
	prog, err := cache.GetOrCompile(jsCode)
	if err != nil {
		t.Fatalf("GetOrCompile failed: %v", err)
	}

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

func TestGoja_ConcurrentExecutionRaceFree(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	jsCode := `
function RegisterUser(params, db) {
	db.insert("accounts", {
		id: params.id,
		holder: params.name,
		balance: params.initial_deposit
	});
	let row = db.get("accounts", params.id);
	return row.balance;
}
`
	cache := runtime.NewPlanCache()
	prog, err := cache.GetOrCompile(jsCode)
	if err != nil {
		t.Fatalf("GetOrCompile failed: %v", err)
	}

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

func TestGoja_EndToEndTSqlTryCatchAndCase(t *testing.T) {
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
	lexer := proc.NewLexer(tsqlScript)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	jsCode, err := proc.Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	prog, err := cache.GetOrCompile(jsCode)
	if err != nil {
		t.Fatalf("GetOrCompile failed: %v", err)
	}

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

func TestGoja_CursorIterationAndBatchProcessing(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// Procedure using cursor to iterate over all accounts and award 10% interest
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
	lexer := proc.NewLexer(tsqlScript)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	jsCode, err := proc.Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	prog, err := cache.GetOrCompile(jsCode)
	if err != nil {
		t.Fatalf("GetOrCompile failed: %v", err)
	}

	executor := runtime.NewExecutor(cache)

	res, err := executor.Run(prog, "ApplyInterest", map[string]any{
		"rate": 0.1,
	}, session)
	if err != nil {
		t.Fatalf("ApplyInterest failed: %v", err)
	}

	// Alice (1000 -> +100 = 1100), Bob (500 -> +50 = 550), Charlie (200 -> +20 = 220) => Total = 170
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

func TestGoja_MultiRowUpdateAndDeleteWhere(t *testing.T) {
	eng := setupTestDB(t)
	defer eng.Close()

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// Procedure doing multi-row update and delete
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
	lexer := proc.NewLexer(tsqlScript)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	jsCode, err := proc.Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	prog, err := cache.GetOrCompile(jsCode)
	if err != nil {
		t.Fatalf("GetOrCompile failed: %v", err)
	}

	executor := runtime.NewExecutor(cache)

	// Threshold = 600: Bob (500 -> 600), Charlie (200 -> 300).
	// Then DELETE WHERE balance <= 300 deletes Charlie (300).
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

func TestGoja_LikeInBetweenOperators(t *testing.T) {
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
	lexer := proc.NewLexer(tsqlScript)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("ParseProcedure failed: %v", err)
	}

	jsCode, err := proc.Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}

	cache := runtime.NewPlanCache()
	prog, err := cache.GetOrCompile(jsCode)
	if err != nil {
		t.Fatalf("GetOrCompile failed: %v", err)
	}

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
