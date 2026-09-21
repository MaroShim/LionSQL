package proc

import (
	"strings"
	"testing"
)

func TestTranspile_BankingTransfer(t *testing.T) {
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
        RAISERROR('Insufficient balance');
        RETURN -1;
    END

    UPDATE accounts SET balance = balance - @amount WHERE id = @from;
    UPDATE accounts SET balance = balance + @amount WHERE id = @to;
    PRINT 'Transfer successful';
    RETURN 0;
END;
`

	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	js, err := Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile error: %v", err)
	}

	expectedSnippets := []string{
		"function TransferFunds(params, db) {",
		"let from = (params && typeof params === 'object') ? (params['from'] !== undefined ? params['from'] : params['@from']) : arguments[0];",
		"let sender_bal = null;",
		"sender_bal = db.get(\"accounts\", from, \"balance\");",
		"if ((sender_bal < amount)) {",
		"throw new Error(\"Insufficient balance\");",
		"db.update(\"accounts\", from, { \"balance\": (_curr[\"balance\"] - amount) });",
		"db.update(\"accounts\", to, { \"balance\": (_curr[\"balance\"] + amount) });",
		"console.log(\"Transfer successful\");",
		"return 0;",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(js, snippet) {
			t.Errorf("Expected JS to contain:\n%s\n\nActual JS:\n%s", snippet, js)
		}
	}
}

func TestTranspile_WhileLoopAndArithmetic(t *testing.T) {
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
END;
`

	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	js, err := Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile error: %v", err)
	}

	expectedSnippets := []string{
		"function ComputeFactorial(params, db) {",
		"let res = null;",
		"let i = null;",
		"res = 1;",
		"i = 1;",
		"while ((i <= n)) {",
		"res = (res * i);",
		"i = (i + 1);",
		"return res;",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(js, snippet) {
			t.Errorf("Expected JS to contain:\n%s\n\nActual JS:\n%s", snippet, js)
		}
	}
}

func TestTranspile_InsertAndDelete(t *testing.T) {
	script := `
CREATE PROCEDURE ManageAccount
    @id INT,
    @holder VARCHAR,
    @balance INT
AS
BEGIN
    INSERT INTO accounts (id, holder, balance) VALUES (@id, @holder, @balance);
    DELETE FROM accounts WHERE id = @id;
    RETURN 0;
END;
`

	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	js, err := Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile error: %v", err)
	}

	expectedSnippets := []string{
		"db.insert(\"accounts\", { \"id\": id, \"holder\": holder, \"balance\": balance });",
		"db.delete(\"accounts\", id);",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(js, snippet) {
			t.Errorf("Expected JS to contain:\n%s\n\nActual JS:\n%s", snippet, js)
		}
	}
}

func TestTranspile_TryCatchAndThrow(t *testing.T) {
	script := `
CREATE PROCEDURE SafeTransfer
    @from INT,
    @to INT,
    @amount INT
AS
BEGIN
    BEGIN TRY
        IF @amount <= 0
        BEGIN
            THROW 50001, 'Amount must be positive', 1;
        END
        UPDATE accounts SET balance = balance - @amount WHERE id = @from;
        UPDATE accounts SET balance = balance + @amount WHERE id = @to;
    END TRY
    BEGIN CATCH
        PRINT 'Error caught in procedure';
        RETURN -1;
    END CATCH
    RETURN 0;
END;
`
	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	js, err := Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile error: %v", err)
	}

	expectedSnippets := []string{
		"try {",
		"throw new Error(\"Amount must be positive\");",
		"} catch (_err) {",
		"console.log(\"Error caught in procedure\");",
		"return (-1);",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(js, snippet) {
			t.Errorf("Expected JS to contain:\n%s\n\nActual JS:\n%s", snippet, js)
		}
	}
}

func TestTranspile_CaseWhenAndBuiltinFunctions(t *testing.T) {
	script := `
CREATE PROCEDURE EvaluateGrade
    @score INT,
    @name VARCHAR(50)
AS
BEGIN
    DECLARE @grade VARCHAR(10);
    DECLARE @clean_name VARCHAR(50);
    SET @clean_name = ISNULL(@name, 'Anonymous');
    SET @grade = CASE 
        WHEN @score >= 90 THEN 'A'
        WHEN @score >= 80 THEN 'B'
        ELSE 'C'
    END;
    PRINT @grade;
    RETURN 0;
END;
`
	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	js, err := Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile error: %v", err)
	}

	expectedSnippets := []string{
		"clean_name = ((name !== null && name !== undefined) ? (name) : (\"Anonymous\"));",
		"grade = (((score >= 90)) ? (\"A\") : ((((score >= 80)) ? (\"B\") : (\"C\"))));",
		"console.log(grade);",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(js, snippet) {
			t.Errorf("Expected JS to contain:\n%s\n\nActual JS:\n%s", snippet, js)
		}
	}
}

func TestTranspile_InBetweenLikeIsNull(t *testing.T) {
	script := `
CREATE PROCEDURE FilterUsers
    @status VARCHAR(20),
    @val INT,
    @name VARCHAR(50)
AS
BEGIN
    IF @status IN ('ACTIVE', 'PENDING')
    BEGIN
        PRINT 'Valid status';
    END

    IF @val BETWEEN 10 AND 100
    BEGIN
        PRINT 'Within range';
    END

    IF @name LIKE 'Ali%'
    BEGIN
        PRINT 'Matches pattern';
    END

    IF @name IS NULL
    BEGIN
        PRINT 'Name is null';
    END

    RETURN 0;
END;
`
	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	js, err := Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile error: %v", err)
	}

	expectedSnippets := []string{
		"if (SQLion.in(status, [\"ACTIVE\", \"PENDING\"]))",
		"if (SQLion.between(val, 10, 100))",
		"if (SQLion.like(name, \"Ali%\"))",
		"if (((name) === null || (name) === undefined))",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(js, snippet) {
			t.Errorf("Expected JS to contain:\n%s\n\nActual JS:\n%s", snippet, js)
		}
	}
}

func TestTranspile_CursorAndIteration(t *testing.T) {
	script := `
CREATE PROCEDURE ProcessAllAccounts
AS
BEGIN
    DECLARE @id INT;
    DECLARE @balance INT;
    DECLARE acc_cur CURSOR FOR SELECT id, balance FROM accounts WHERE balance > 0;
    
    OPEN acc_cur;
    FETCH NEXT FROM acc_cur INTO @id, @balance;
    
    WHILE @@FETCH_STATUS = 0
    BEGIN
        PRINT @id;
        FETCH NEXT FROM acc_cur INTO @id, @balance;
    END

    CLOSE acc_cur;
    DEALLOCATE acc_cur;
    RETURN 0;
END;
`
	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	js, err := Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile error: %v", err)
	}

	expectedSnippets := []string{
		"let _cur_acc_cur = { index: 0, columns: [\"id\", \"balance\"], rows: db.query(\"accounts\", function(row) { return (row[\"balance\"] > 0); }) };",
		"if (_cur_acc_cur) { _cur_acc_cur.index = 0; }",
		"if (_cur_acc_cur && _cur_acc_cur.index < _cur_acc_cur.rows.length)",
		"while ((_fetch_status === 0))",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(js, snippet) {
			t.Errorf("Expected JS to contain:\n%s\n\nActual JS:\n%s", snippet, js)
		}
	}
}

func TestTranspile_MultiRowUpdateAndDeleteWhere(t *testing.T) {
	script := `
CREATE PROCEDURE MassUpdate
    @min_bal INT
AS
BEGIN
    UPDATE accounts SET balance = balance + 50 WHERE balance < @min_bal;
    DELETE FROM accounts WHERE balance <= 0;
    RETURN 0;
END;
`
	lexer := NewLexer(script)
	parser := NewParser(lexer)
	plan, err := parser.ParseProcedure()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	js, err := Transpile(plan)
	if err != nil {
		t.Fatalf("Transpile error: %v", err)
	}

	expectedSnippets := []string{
		"db.updateWhere(\"accounts\", function(row) { return (row[\"balance\"] < min_bal); }",
		"db.deleteWhere(\"accounts\", function(row) { return (row[\"balance\"] <= 0); });",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(js, snippet) {
			t.Errorf("Expected JS to contain:\n%s\n\nActual JS:\n%s", snippet, js)
		}
	}
}
