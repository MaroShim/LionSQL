package api_test

import (
	"testing"
)

func TestDynamicSQL_SpExecuteSql_Basic(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE users (
		id INT PRIMARY KEY,
		name VARCHAR(50) NOT NULL,
		dept VARCHAR(50) NOT NULL,
		salary FLOAT NOT NULL
	);`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = server.ExecuteSQL(`INSERT INTO users (id, name, dept, salary) VALUES
		(1, 'Alice', 'Engineering', 1000.0),
		(2, 'Bob', 'Engineering', 1200.0),
		(3, 'Charlie', 'Sales', 900.0);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 1. Basic sp_executesql without parameters
	res, err := server.ExecuteSQL(`EXEC sp_executesql N'SELECT * FROM users WHERE id = 1';`)
	if err != nil || !res.Success {
		t.Fatalf("sp_executesql without params failed: %v (res: %+v)", err, res)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Fatalf("unexpected rows: %+v", res.Rows)
	}

	// 2. Named parameter binding
	res, err = server.ExecuteSQL(`EXEC sp_executesql N'SELECT name, salary FROM users WHERE id = @id', N'@id INT', @id = 2;`)
	if err != nil || !res.Success {
		t.Fatalf("sp_executesql named params failed: %v (res: %+v)", err, res)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Bob" {
		t.Fatalf("unexpected rows: %+v", res.Rows)
	}

	// 3. Multiple parameters (named & string literal with quotes)
	res, err = server.ExecuteSQL(`EXEC sp_executesql N'SELECT name FROM users WHERE dept = @d AND salary > @s', N'@d VARCHAR(50), @s FLOAT', @d = 'Engineering', @s = 1100.0;`)
	if err != nil || !res.Success {
		t.Fatalf("sp_executesql multi-param failed: %v (res: %+v)", err, res)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Bob" {
		t.Fatalf("expected Bob, got: %+v", res.Rows)
	}

	// 4. Positional parameter binding
	res, err = server.ExecuteSQL(`EXEC sp_executesql N'SELECT name FROM users WHERE id = @uid', N'@uid INT', 3;`)
	if err != nil || !res.Success {
		t.Fatalf("sp_executesql positional param failed: %v (res: %+v)", err, res)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Charlie" {
		t.Fatalf("expected Charlie, got: %+v", res.Rows)
	}
}

func TestDynamicSQL_ExecStringLiteral(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE items (id INT PRIMARY KEY, title VARCHAR(50));`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 1. EXEC ('...') DML statement
	res, err := server.ExecuteSQL(`EXEC ('INSERT INTO items (id, title) VALUES (10, ''Keyboard'');');`)
	if err != nil || !res.Success {
		t.Fatalf("EXEC ('INSERT...') failed: %v", err)
	}

	// 2. EXEC (N'...') query statement
	res, err = server.ExecuteSQL(`EXEC (N'SELECT * FROM items WHERE id = 10;');`)
	if err != nil || !res.Success {
		t.Fatalf("EXEC (N'SELECT...') failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "Keyboard" {
		t.Fatalf("expected Keyboard, got: %+v", res.Rows)
	}

	// 3. EXEC ('SELECT 1 AS n') without semicolon
	res, err = server.ExecuteSQL(`EXEC ('SELECT 42 AS answer')`)
	if err != nil || !res.Success {
		t.Fatalf("EXEC ('SELECT 42') failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", res.Rows)
	}
}

func TestDynamicSQL_SessionVariables(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	sessID := "dyn_session_1"

	_, err := server.ExecuteSessionSQL(sessID, `CREATE TABLE logs (id INT PRIMARY KEY, message VARCHAR(100));`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Set dynamic query string variable
	_, err = server.ExecuteSessionSQL(sessID, `SET @dynQuery = 'INSERT INTO logs (id, message) VALUES (1, ''System Started'');';`)
	if err != nil {
		t.Fatalf("SET variable failed: %v", err)
	}

	// Execute variable via EXEC (@dynQuery)
	res, err := server.ExecuteSessionSQL(sessID, `EXEC (@dynQuery);`)
	if err != nil || !res.Success {
		t.Fatalf("EXEC (@dynQuery) failed: %v (res: %+v)", err, res)
	}

	// Verify row inserted
	res, err = server.ExecuteSessionSQL(sessID, `SELECT * FROM logs;`)
	if err != nil || !res.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["message"] != "System Started" {
		t.Fatalf("expected System Started, got: %+v", res.Rows)
	}
}

func TestDynamicSQL_StoredProcedureWithDynamicExec(t *testing.T) {
	server, cleanup := setupTestServer()
	defer cleanup()

	_, err := server.ExecuteSQL(`CREATE TABLE audit (id INT PRIMARY KEY, info VARCHAR(100));`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// Create stored procedure that uses dynamic EXEC (@cmd)
	procSQL := `CREATE PROCEDURE RunDynamicCmd
		@msg VARCHAR(100)
	AS
	BEGIN
		DECLARE @sql VARCHAR(200);
		SET @sql = 'INSERT INTO audit (id, info) VALUES (99, ''' + @msg + ''');';
		EXEC (@sql);
	END;`
	_, err = server.ExecuteSQL(procSQL)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE with dynamic EXEC failed: %v", err)
	}

	// Call the procedure
	res, err := server.ExecuteSQL(`EXEC RunDynamicCmd 'dynamic audit log';`)
	if err != nil || !res.Success {
		t.Fatalf("EXEC RunDynamicCmd failed: %v", err)
	}

	// Check table contents
	selectRes, err := server.ExecuteSQL(`SELECT * FROM audit WHERE id = 99;`)
	if err != nil || !selectRes.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(selectRes.Rows) != 1 || selectRes.Rows[0]["info"] != "dynamic audit log" {
		t.Fatalf("expected 'dynamic audit log', got: %+v", selectRes.Rows)
	}
}
