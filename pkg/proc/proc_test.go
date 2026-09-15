package proc

import (
	"strings"
	"testing"

	"sqlserver/pkg/storage"
	"sqlserver/pkg/storage/memengine"
)

func setupBank(t *testing.T) (storage.Engine, storage.Session) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	schema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar, Nullable: false},
			{Name: "balance", Type: storage.TypeInt, Nullable: false},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	// Seed accounts: 1 (Alice, 1000), 2 (Bob, 500)
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
	if err := tx.Commit(); err != nil {
		t.Fatalf("Seed commit failed: %v", err)
	}

	return eng, session
}

func TestProcedure_BankingTransferSuccess(t *testing.T) {
	eng, session := setupBank(t)
	defer eng.Close()
	defer session.Close()

	script := `
CREATE PROCEDURE TransferFunds
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    DECLARE @sender_bal INT;
    SELECT balance INTO @sender_bal FROM accounts WHERE id = @from;

    IF @sender_bal < @amount
    BEGIN
        RAISERROR('Insufficient funds in sender account');
        RETURN -1;
    END

    UPDATE accounts SET balance = balance - @amount WHERE id = @from;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to;
    PRINT 'Transfer successful';
    RETURN 0;
END
`

	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	args := map[string]storage.Value{
		"@from":   storage.IntValue(1),
		"@to":     storage.IntValue(2),
		"@amount": storage.IntValue(300),
	}

	res, err := Run(plan, args, session)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if res.ReturnValue.IVal != 0 {
		t.Errorf("Expected return code 0, got %d", res.ReturnValue.IVal)
	}
	if len(res.Printed) == 0 || res.Printed[0] != "Transfer successful" {
		t.Errorf("Expected print 'Transfer successful', got %v", res.Printed)
	}

	// Verify balances
	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	alice, err := tx.Get("accounts", storage.IntValue(1))
	if err != nil || alice["balance"].IVal != 700 {
		t.Fatalf("Expected Alice balance 700, got %+v (err: %v)", alice, err)
	}

	bob, err := tx.Get("accounts", storage.IntValue(2))
	if err != nil || bob["balance"].IVal != 800 {
		t.Fatalf("Expected Bob balance 800, got %+v (err: %v)", bob, err)
	}
}

func TestProcedure_BankingTransferRollbackOnInsufficientFunds(t *testing.T) {
	eng, session := setupBank(t)
	defer eng.Close()
	defer session.Close()

	script := `
CREATE PROCEDURE TransferFunds
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    DECLARE @sender_bal INT;
    SELECT balance INTO @sender_bal FROM accounts WHERE id = @from;

    IF @sender_bal < @amount
    BEGIN
        RAISERROR('Insufficient funds in sender account');
        RETURN -1;
    END

    UPDATE accounts SET balance = balance - @amount WHERE id = @from;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to;
    RETURN 0;
END
`

	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Alice only has 1000, attempt to transfer 2000
	args := map[string]storage.Value{
		"@from":   storage.IntValue(1),
		"@to":     storage.IntValue(2),
		"@amount": storage.IntValue(2000),
	}

	_, err = Run(plan, args, session)
	if err == nil {
		t.Fatalf("Expected error due to insufficient funds, but got nil")
	}
	if !strings.Contains(err.Error(), "Insufficient funds") {
		t.Fatalf("Expected 'Insufficient funds' in error, got: %v", err)
	}

	// Verify ambient transaction rolled back cleanly! Alice and Bob have original balances
	tx, err := session.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	defer tx.Rollback()

	alice, err := tx.Get("accounts", storage.IntValue(1))
	if err != nil || alice["balance"].IVal != 1000 {
		t.Fatalf("Expected Alice balance to remain 1000, got %+v", alice)
	}

	bob, err := tx.Get("accounts", storage.IntValue(2))
	if err != nil || bob["balance"].IVal != 500 {
		t.Fatalf("Expected Bob balance to remain 500, got %+v", bob)
	}
}

func TestProcedure_WhileLoopAndCalculation(t *testing.T) {
	eng, session := setupBank(t)
	defer eng.Close()
	defer session.Close()

	script := `
CREATE PROCEDURE ComputeFactorial
    @n INT
AS
BEGIN
    DECLARE @res INT;
    DECLARE @i INT;
    SET @res = 1;
    SET @i = 1;

    WHILE @i <= @n
    BEGIN
        SET @res = @res * @i;
        SET @i = @i + 1;
    END

    RETURN @res;
END
`

	cache := NewPlanCache()
	plan, err := cache.GetOrCompile(script)
	if err != nil {
		t.Fatalf("GetOrCompile error: %v", err)
	}

	args := map[string]storage.Value{
		"@n": storage.IntValue(5),
	}

	res, err := Run(plan, args, session)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 5! = 120
	if res.ReturnValue.IVal != 120 {
		t.Errorf("Expected 5! = 120, got %d", res.ReturnValue.IVal)
	}
}
