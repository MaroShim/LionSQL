package runtime_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"sqlion/pkg/proc"
	antlrParser "sqlion/pkg/proc/parser/antlr"
	"sqlion/pkg/proc/runtime"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

// setupRegressionDB populates a rich schema with products, orders, and customer accounts for testing.
func setupRegressionDB(t *testing.T) storage.Engine {
	eng := memengine.New()

	// 1. Accounts Table
	if err := eng.CreateTable(&storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "holder", Type: storage.TypeVarChar},
			{Name: "balance", Type: storage.TypeInt},
			{Name: "tier", Type: storage.TypeVarChar},
			{Name: "status", Type: storage.TypeVarChar},
		},
	}); err != nil {
		t.Fatalf("CreateTable accounts failed: %v", err)
	}

	// 2. Products Table
	if err := eng.CreateTable(&storage.TableSchema{
		Name: "products",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
			{Name: "category", Type: storage.TypeVarChar},
			{Name: "price", Type: storage.TypeFloat},
			{Name: "stock", Type: storage.TypeInt},
		},
	}); err != nil {
		t.Fatalf("CreateTable products failed: %v", err)
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

	// Seed accounts
	_ = tx.Insert("accounts", storage.Record{"id": storage.IntValue(1), "holder": storage.StringValue("Alice"), "balance": storage.IntValue(1000), "tier": storage.StringValue("VIP"), "status": storage.StringValue("ACTIVE")})
	_ = tx.Insert("accounts", storage.Record{"id": storage.IntValue(2), "holder": storage.StringValue("Bob"), "balance": storage.IntValue(500), "tier": storage.StringValue("GOLD"), "status": storage.StringValue("ACTIVE")})
	_ = tx.Insert("accounts", storage.Record{"id": storage.IntValue(3), "holder": storage.StringValue("Charlie"), "balance": storage.IntValue(200), "tier": storage.StringValue("STANDARD"), "status": storage.StringValue("SUSPENDED")})
	_ = tx.Insert("accounts", storage.Record{"id": storage.IntValue(4), "holder": storage.StringValue("Diana"), "balance": storage.IntValue(50), "tier": storage.NullValue(storage.TypeVarChar), "status": storage.StringValue("ACTIVE")})

	// Seed products
	_ = tx.Insert("products", storage.Record{"id": storage.IntValue(101), "name": storage.StringValue("Laptop Pro"), "category": storage.StringValue("Electronics"), "price": storage.FloatValue(1200.0), "stock": storage.IntValue(10)})
	_ = tx.Insert("products", storage.Record{"id": storage.IntValue(102), "name": storage.StringValue("Wireless Mouse"), "category": storage.StringValue("Electronics"), "price": storage.FloatValue(25.5), "stock": storage.IntValue(50)})
	_ = tx.Insert("products", storage.Record{"id": storage.IntValue(103), "name": storage.StringValue("Coffee Mug"), "category": storage.StringValue("Kitchen"), "price": storage.FloatValue(12.0), "stock": storage.IntValue(100)})
	_ = tx.Insert("products", storage.Record{"id": storage.IntValue(104), "name": storage.StringValue("Desk Lamp"), "category": storage.StringValue("Office"), "price": storage.FloatValue(45.0), "stock": storage.IntValue(0)})

	if err := tx.Commit(); err != nil {
		t.Fatalf("Seed commit failed: %v", err)
	}

	return eng
}

// Helper to compile and run a procedure through the Pratt parser
func runProc(t *testing.T, eng storage.Engine, tsql string, procName string, params map[string]any) (*runtime.ExecutionResult, error) {
	lexer := proc.NewLexer(tsql)
	parser := proc.NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		return nil, fmt.Errorf("parser error: %w", err)
	}

	jsCode, err := proc.Transpile(plan)
	if err != nil {
		return nil, fmt.Errorf("transpiler error: %w", err)
	}

	cache := runtime.NewPlanCache()
	prog, err := cache.GetOrCompile(jsCode)
	if err != nil {
		return nil, fmt.Errorf("compile error: %w", err)
	}

	session, err := eng.NewSession()
	if err != nil {
		return nil, fmt.Errorf("session error: %w", err)
	}
	defer session.Close()

	executor := runtime.NewExecutor(cache)
	return executor.Run(prog, procName, params, session)
}

// 1. Regression: Multi-Variable Declarations, Complex Arithmetic & Operator Precedence
func TestRegression_MultiVariableArithmeticAndPrecedence(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE CalculateComplexMath
    @x INT,
    @y INT,
    @z INT
AS
BEGIN
    DECLARE @res INT;
    DECLARE @neg INT;
    DECLARE @div INT;
    DECLARE @str VARCHAR(100);

    SET @res = (@x + @y) * @z - 10;
    SET @neg = -@x * 2;
    SET @div = @y / 2;
    SET @str = 'Result: ' + @res;

    PRINT @str;
    RETURN @res + @neg + @div;
END;
`
	// x=10, y=20, z=5:
	// @res = (10 + 20) * 5 - 10 = 150 - 10 = 140
	// @neg = -10 * 2 = -20
	// @div = 20 / 2 = 10
	// Return: 140 + (-20) + 10 = 130
	res, err := runProc(t, eng, tsql, "CalculateComplexMath", map[string]any{
		"x": 10,
		"y": 20,
		"z": 5,
	})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if fmt.Sprintf("%v", res.ReturnValue) != "130" {
		t.Errorf("expected return value 130, got %v", res.ReturnValue)
	}
	if len(res.Logs) != 1 || !strings.Contains(res.Logs[0], "Result: 140") {
		t.Errorf("expected log 'Result: 140', got %v", res.Logs)
	}
}

// 2. Regression: Nested IF-ELSE, WHILE loops with BREAK and CONTINUE
func TestRegression_NestedControlFlowAndLoops(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE LoopWithBranching
    @max_iters INT,
    @skip_val INT,
    @stop_val INT
AS
BEGIN
    DECLARE @sum INT;
    DECLARE @i INT;
    SET @sum = 0;
    SET @i = 0;

    WHILE @i < @max_iters
    BEGIN
        SET @i = @i + 1;
        IF @i = @skip_val
        BEGIN
            CONTINUE;
        END

        IF @i = @stop_val
        BEGIN
            BREAK;
        END

        IF @i > 5
        BEGIN
            SET @sum = @sum + (@i * 2);
        END
        ELSE
        BEGIN
            SET @sum = @sum + @i;
        END
    END

    RETURN @sum;
END;
`
	// max=10, skip=3, stop=8
	// i=1: sum += 1 = 1
	// i=2: sum += 2 = 3
	// i=3: skipped (sum=3)
	// i=4: sum += 4 = 7
	// i=5: sum += 5 = 12
	// i=6: sum += (6*2) = 12 -> 24
	// i=7: sum += (7*2) = 14 -> 38
	// i=8: break
	// Expected sum = 38
	res, err := runProc(t, eng, tsql, "LoopWithBranching", map[string]any{
		"max_iters": 10,
		"skip_val":  3,
		"stop_val":  8,
	})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if fmt.Sprintf("%v", res.ReturnValue) != "38" {
		t.Errorf("expected return value 38, got %v", res.ReturnValue)
	}
}

// 3. Regression: Full Suite of Built-In Scalar Functions
func TestRegression_AllBuiltinScalarFunctions(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE TestStringAndMathFuncs
    @raw_str VARCHAR(50),
    @val FLOAT
AS
BEGIN
    DECLARE @u VARCHAR(50);
    DECLARE @l VARCHAR(50);
    DECLARE @t VARCHAR(50);
    DECLARE @sub VARCHAR(50);
    DECLARE @rep VARCHAR(50);
    DECLARE @len INT;
    DECLARE @abs_val FLOAT;
    DECLARE @rnd FLOAT;
    DECLARE @ceil FLOAT;
    DECLARE @flr FLOAT;

    SET @u = UPPER(@raw_str);
    SET @l = LOWER(@raw_str);
    SET @t = TRIM(@raw_str);
    SET @len = LEN(@t);
    SET @sub = SUBSTRING(@t, 1, 4);
    SET @rep = REPLACE(@t, 'World', 'SQLion');

    SET @abs_val = ABS(@val);
    SET @rnd = ROUND(@val);
    SET @ceil = CEILING(@val);
    SET @flr = FLOOR(@val);

    PRINT @u;
    PRINT @l;
    PRINT @t;
    PRINT @sub;
    PRINT @rep;

    RETURN @len;
END;
`
	res, err := runProc(t, eng, tsql, "TestStringAndMathFuncs", map[string]any{
		"raw_str": "  Hello World  ",
		"val":     -42.7,
	})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	// Trimmed length of "Hello World" is 11
	if fmt.Sprintf("%v", res.ReturnValue) != "11" {
		t.Errorf("expected len 11, got %v", res.ReturnValue)
	}

	expectedLogs := []string{
		"HELLO WORLD",
		"hello world",
		"Hello World",
		"Hell",
		"Hello SQLion",
	}
	for i, expected := range expectedLogs {
		if i >= len(res.Logs) || !strings.Contains(res.Logs[i], expected) {
			t.Errorf("log[%d] expected to contain %q, got %v", i, expected, res.Logs)
		}
	}
}

// 4. Regression: ISNULL & COALESCE Null-Coalescing Logic
func TestRegression_IsNullAndCoalesce(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE CheckNullCoalesce
    @id INT
AS
BEGIN
    DECLARE @tier VARCHAR(50);
    DECLARE @coalesced VARCHAR(50);
    DECLARE @is_null_res VARCHAR(50);

    SET @tier = (SELECT tier FROM accounts WHERE id = @id);
    SET @is_null_res = ISNULL(@tier, 'DEFAULT_ISNULL');
    SET @coalesced = COALESCE(@tier, 'FALLBACK_COALESCE');

    PRINT @is_null_res;
    PRINT @coalesced;
    RETURN 0;
END;
`
	// Diana (id=4) has NULL tier
	res, err := runProc(t, eng, tsql, "CheckNullCoalesce", map[string]any{"id": 4})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if len(res.Logs) < 2 {
		t.Fatalf("expected 2 logs, got %v", res.Logs)
	}
	if res.Logs[0] != "DEFAULT_ISNULL" {
		t.Errorf("expected 'DEFAULT_ISNULL', got %v", res.Logs[0])
	}
	if res.Logs[1] != "FALLBACK_COALESCE" {
		t.Errorf("expected 'FALLBACK_COALESCE', got %v", res.Logs[1])
	}
}

// 5. Regression: Advanced Predicates (IN, NOT IN, BETWEEN, NOT BETWEEN, LIKE, NOT LIKE, IS NULL, IS NOT NULL)
func TestRegression_AdvancedPredicates(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE EvaluatePredicates
    @name VARCHAR(50),
    @score INT,
    @role VARCHAR(50),
    @tag VARCHAR(50)
AS
BEGIN
    DECLARE @in_pass INT;
    DECLARE @between_pass INT;
    DECLARE @like_pass INT;
    DECLARE @null_pass INT;

    SET @in_pass = 0;
    SET @between_pass = 0;
    SET @like_pass = 0;
    SET @null_pass = 0;

    IF @role IN ('ADMIN', 'SUPERUSER', 'ROOT')
    BEGIN
        SET @in_pass = 1;
    END

    IF @score BETWEEN 80 AND 100
    BEGIN
        SET @between_pass = 1;
    END

    IF @name LIKE 'Admin_%'
    BEGIN
        SET @like_pass = 1;
    END

    IF @tag IS NULL
    BEGIN
        SET @null_pass = 1;
    END

    RETURN @in_pass + @between_pass + @like_pass + @null_pass;
END;
`
	// Match all 4 conditions
	resAll, err := runProc(t, eng, tsql, "EvaluatePredicates", map[string]any{
		"name":  "Admin_Bob",
		"score": 95,
		"role":  "ADMIN",
		"tag":   nil,
	})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if fmt.Sprintf("%v", resAll.ReturnValue) != "4" {
		t.Errorf("expected 4 matched predicates, got %v", resAll.ReturnValue)
	}

	// Match none of the conditions
	resNone, err := runProc(t, eng, tsql, "EvaluatePredicates", map[string]any{
		"name":  "Guest_Bob",
		"score": 75,
		"role":  "USER",
		"tag":   "VERIFIED",
	})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if fmt.Sprintf("%v", resNone.ReturnValue) != "0" {
		t.Errorf("expected 0 matched predicates, got %v", resNone.ReturnValue)
	}
}

// 6. Regression: Multi-Row UPDATE, DELETE, and @@ROWCOUNT Validation
func TestRegression_MultiRowDMLAndRowCount(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE BulkDiscountAndPrune
    @category VARCHAR(50),
    @discount FLOAT
AS
BEGIN
    DECLARE @updated_rows INT;
    DECLARE @deleted_rows INT;

    UPDATE products SET price = price - @discount WHERE category = @category AND stock > 0;
    SET @updated_rows = @@ROWCOUNT;

    DELETE FROM products WHERE stock = 0;
    SET @deleted_rows = @@ROWCOUNT;

    PRINT 'Updated: ' + @updated_rows;
    PRINT 'Deleted: ' + @deleted_rows;

    RETURN @updated_rows + @deleted_rows;
END;
`
	// Category 'Electronics' with stock > 0 has 2 items (101: Laptop, 102: Mouse)
	// Stock = 0 has 1 item (104: Desk Lamp)
	// Total affected = 2 + 1 = 3
	res, err := runProc(t, eng, tsql, "BulkDiscountAndPrune", map[string]any{
		"category": "Electronics",
		"discount": 5.0,
	})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if fmt.Sprintf("%v", res.ReturnValue) != "3" {
		t.Errorf("expected return value 3, got %v", res.ReturnValue)
	}

	session, _ := eng.NewSession()
	defer session.Close()
	tx, _ := session.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx.Rollback()

	// Verify price updates
	laptop, _ := tx.Get("products", storage.IntValue(101))
	if laptop["price"].FVal != 1195.0 {
		t.Errorf("expected Laptop price 1195.0, got %v", laptop["price"].FVal)
	}

	mouse, _ := tx.Get("products", storage.IntValue(102))
	if mouse["price"].FVal != 20.5 {
		t.Errorf("expected Mouse price 20.5, got %v", mouse["price"].FVal)
	}

	// Verify desk lamp deletion
	_, errLamp := tx.Get("products", storage.IntValue(104))
	if !errors.Is(errLamp, storage.ErrKeyNotFound) {
		t.Errorf("expected Desk Lamp to be pruned, got %v", errLamp)
	}
}

// 7. Regression: Cursor-Based Sequential Processing with @@FETCH_STATUS
func TestRegression_CursorLifecycle(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE AuditAndReactivate
AS
BEGIN
    DECLARE @id INT;
    DECLARE @status VARCHAR(20);
    DECLARE @count INT;
    SET @count = 0;

    DECLARE cur CURSOR FOR SELECT id, status FROM accounts WHERE status = 'SUSPENDED';
    OPEN cur;
    FETCH NEXT FROM cur INTO @id, @status;

    WHILE @@FETCH_STATUS = 0
    BEGIN
        UPDATE accounts SET status = 'ACTIVE' WHERE id = @id;
        SET @count = @count + 1;
        FETCH NEXT FROM cur INTO @id, @status;
    END

    CLOSE cur;
    DEALLOCATE cur;
    RETURN @count;
END;
`
	// Charlie (id=3) is SUSPENDED. Should reactivate 1 account.
	res, err := runProc(t, eng, tsql, "AuditAndReactivate", nil)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if fmt.Sprintf("%v", res.ReturnValue) != "1" {
		t.Errorf("expected 1 reactivated account, got %v", res.ReturnValue)
	}

	session, _ := eng.NewSession()
	defer session.Close()
	tx, _ := session.BeginTx(storage.TxOptions{ReadOnly: true})
	charlie, _ := tx.Get("accounts", storage.IntValue(3))
	_ = tx.Rollback()

	if charlie["status"].SVal != "ACTIVE" {
		t.Errorf("expected Charlie status ACTIVE, got %s", charlie["status"].SVal)
	}
}

// 8. Regression: TRY-CATCH Error Interception and ACID Rollback Safety
func TestRegression_TryCatchAndAcidRollback(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE SafeTransferWithRecovery
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    DECLARE @from_bal INT;
    BEGIN TRY
        SET @from_bal = (SELECT balance FROM accounts WHERE id = @from);
        IF @from_bal < @amount
        BEGIN
            THROW 'Insufficient funds for transfer';
        END

        UPDATE accounts SET balance = balance - @amount WHERE id = @from;
        UPDATE accounts SET balance = balance + @amount WHERE id = @to;
        RETURN 1;
    END TRY
    BEGIN CATCH
        PRINT 'Caught transfer error, performing graceful degradation';
        RETURN -1;
    END CATCH
END;
`
	// 1. Success case: Transfer $200 from Alice (1) to Bob (2)
	res1, err := runProc(t, eng, tsql, "SafeTransferWithRecovery", map[string]any{
		"from":   1,
		"to":     2,
		"amount": 200,
	})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if fmt.Sprintf("%v", res1.ReturnValue) != "1" {
		t.Errorf("expected return value 1 (success), got %v", res1.ReturnValue)
	}

	// 2. Failure case: Transfer $5000 from Alice (1)
	res2, err := runProc(t, eng, tsql, "SafeTransferWithRecovery", map[string]any{
		"from":   1,
		"to":     2,
		"amount": 5000,
	})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if fmt.Sprintf("%v", res2.ReturnValue) != "-1" {
		t.Errorf("expected return value -1 (caught in catch), got %v", res2.ReturnValue)
	}
	if len(res2.Logs) != 1 || !strings.Contains(res2.Logs[0], "Caught transfer error") {
		t.Errorf("expected catch log, got %v", res2.Logs)
	}

	// Verify database state: Alice=800, Bob=700 (unchanged by second failed attempt)
	session, _ := eng.NewSession()
	defer session.Close()
	tx, _ := session.BeginTx(storage.TxOptions{ReadOnly: true})
	defer tx.Rollback()

	alice, _ := tx.Get("accounts", storage.IntValue(1))
	bob, _ := tx.Get("accounts", storage.IntValue(2))
	if alice["balance"].IVal != 800 {
		t.Errorf("expected Alice balance 800, got %d", alice["balance"].IVal)
	}
	if bob["balance"].IVal != 700 {
		t.Errorf("expected Bob balance 700, got %d", bob["balance"].IVal)
	}
}

// 9. Regression: Searched CASE-WHEN Expressions with Multiple Branches
func TestRegression_SearchedCaseWhenMultiBranch(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE CategorizeOrder
    @total_amount FLOAT
AS
BEGIN
    DECLARE @category VARCHAR(20);
    SET @category = CASE
        WHEN @total_amount >= 1000.0 THEN 'PLATINUM'
        WHEN @total_amount >= 500.0 THEN 'GOLD'
        WHEN @total_amount >= 100.0 THEN 'SILVER'
        ELSE 'BRONZE'
    END;

    PRINT @category;
    RETURN 0;
END;
`
	testCases := []struct {
		amount   float64
		expected string
	}{
		{1500.0, "PLATINUM"},
		{750.0, "GOLD"},
		{250.0, "SILVER"},
		{49.99, "BRONZE"},
	}

	for _, tc := range testCases {
		res, err := runProc(t, eng, tsql, "CategorizeOrder", map[string]any{
			"total_amount": tc.amount,
		})
		if err != nil {
			t.Fatalf("amount %f failed: %v", tc.amount, err)
		}
		if len(res.Logs) != 1 || res.Logs[0] != tc.expected {
			t.Errorf("amount %f: expected %s, got %v", tc.amount, tc.expected, res.Logs)
		}
	}
}

// 10. Regression: ANTLR4 Parser & Native Pratt Parser Dual Parity
func TestRegression_AntlrAndPrattParserDualParity(t *testing.T) {
	eng := setupRegressionDB(t)
	defer eng.Close()

	tsql := `
CREATE PROCEDURE CalculateVIPDiscounts
    @min_balance INT,
    @rate FLOAT
AS
BEGIN
    DECLARE @id INT;
    DECLARE @holder VARCHAR(50);
    DECLARE @bal INT;
    DECLARE @total_discount INT;
    SET @total_discount = 0;

    DECLARE cur CURSOR FOR SELECT id, holder, balance FROM accounts WHERE balance >= @min_balance;
    OPEN cur;
    FETCH NEXT FROM cur INTO @id, @holder, @bal;

    WHILE @@FETCH_STATUS = 0
    BEGIN
        DECLARE @disc INT;
        SET @disc = @bal * @rate;
        SET @total_discount = @total_discount + @disc;
        FETCH NEXT FROM cur INTO @id, @holder, @bal;
    END

    CLOSE cur;
    DEALLOCATE cur;
    RETURN @total_discount;
END;
`
	// 1. Pratt Parser Pipeline
	resPratt, err := runProc(t, eng, tsql, "CalculateVIPDiscounts", map[string]any{
		"min_balance": 500,
		"rate":        0.05,
	})
	if err != nil {
		t.Fatalf("Pratt pipeline failed: %v", err)
	}

	// 2. Official ANTLR4 Parser Pipeline
	antlrPlan, antlrErr := antlrParser.BuildProcedure(tsql)
	if antlrErr != nil {
		t.Fatalf("ANTLR BuildProcedure error: %v", antlrErr)
	}
	antlrJS, transpileErr := proc.Transpile(antlrPlan)
	if transpileErr != nil {
		t.Fatalf("ANTLR Transpile error: %v", transpileErr)
	}

	cache := runtime.NewPlanCache()
	prog, compErr := cache.GetOrCompile(antlrJS)
	if compErr != nil {
		t.Fatalf("ANTLR GetOrCompile error: %v", compErr)
	}

	session, _ := eng.NewSession()
	defer session.Close()
	executor := runtime.NewExecutor(cache)
	resAntlr, err := executor.Run(prog, "CalculateVIPDiscounts", map[string]any{
		"min_balance": 500,
		"rate":        0.05,
	}, session)
	if err != nil {
		t.Fatalf("ANTLR pipeline run failed: %v", err)
	}

	// Alice (1000 * 0.05 = 50) + Bob (500 * 0.05 = 25) = 75
	if fmt.Sprintf("%v", resPratt.ReturnValue) != "75" {
		t.Errorf("Pratt expected 75, got %v", resPratt.ReturnValue)
	}
	if fmt.Sprintf("%v", resAntlr.ReturnValue) != "75" {
		t.Errorf("ANTLR expected 75, got %v", resAntlr.ReturnValue)
	}
}
