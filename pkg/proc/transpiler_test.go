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

	// Assert essential JS structures
	expectedSnippets := []string{
		"function TransferFunds(params, db) {",
		"let from = params['from'] !== undefined ? params['from'] : params['@from'];",
		"let sender_bal = null;",
		"sender_bal = db.get(\"accounts\", from, \"balance\");",
		"if ((sender_bal < amount)) {",
		"throw new Error(\"Insufficient balance\");",
		"db.update(\"accounts\", from, { \"balance\": (row[\"balance\"] - amount) });",
		"db.update(\"accounts\", to, { \"balance\": (row[\"balance\"] + amount) });",
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
