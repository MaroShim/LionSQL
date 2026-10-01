package api_test

import (
	"testing"
)

func TestDynamicSQL_ExecLiteralAndVariable(t *testing.T) {
	s, cleanup := setupTestServer()
	defer cleanup()

	// 1. Setup table and initial data
	_, err := s.ExecuteSQL("CREATE TABLE dyn_users (id INT PRIMARY KEY, name VARCHAR(50), age INT);")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	_, err = s.ExecuteSQL("INSERT INTO dyn_users (id, name, age) VALUES (1, 'Alice', 30), (2, 'Bob', 25);")
	if err != nil {
		t.Fatalf("failed to insert data: %v", err)
	}

	// 2. Direct string literal EXEC ('...')
	res, err := s.ExecuteSQL("EXEC ('SELECT * FROM dyn_users WHERE id = 1');")
	if err != nil {
		t.Fatalf("EXEC ('...') failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Fatalf("unexpected result from EXEC ('...'): %+v", res.Rows)
	}

	// 3. EXEC with variable in session
	sessID := "sess1"
	_, err = s.ExecuteSessionSQL(sessID, "DECLARE @sql VARCHAR(200) = 'SELECT * FROM dyn_users WHERE age = 25';")
	if err != nil {
		t.Fatalf("DECLARE failed: %v", err)
	}

	// EXEC (@sql)
	res, err = s.ExecuteSessionSQL(sessID, "EXEC (@sql);")
	if err != nil {
		t.Fatalf("EXEC (@sql) failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Bob" {
		t.Fatalf("unexpected result from EXEC (@sql): %+v", res.Rows)
	}

	// EXEC @sql (without parentheses)
	res, err = s.ExecuteSessionSQL(sessID, "EXEC @sql;")
	if err != nil {
		t.Fatalf("EXEC @sql (without parentheses) failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["name"] != "Bob" {
		t.Fatalf("unexpected result from EXEC @sql: %+v", res.Rows)
	}
}

func TestDynamicSQL_SpExecuteSQL(t *testing.T) {
	s, cleanup := setupTestServer()
	defer cleanup()

	_, err := s.ExecuteSQL("CREATE TABLE dyn_products (id INT PRIMARY KEY, title VARCHAR(50), price INT);")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	_, err = s.ExecuteSQL("INSERT INTO dyn_products (id, title, price) VALUES (1, 'Book', 15), (2, 'Pen', 3);")
	if err != nil {
		t.Fatalf("failed to insert data: %v", err)
	}

	// 1. Positional with literal @stmt, @params, and named args
	res, err := s.ExecuteSQL("EXEC sp_executesql N'SELECT * FROM dyn_products WHERE id = @id', N'@id INT', @id = 1;")
	if err != nil {
		t.Fatalf("sp_executesql named param failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "Book" {
		t.Fatalf("unexpected result: %+v", res.Rows)
	}

	// 2. Named @stmt and @params
	res, err = s.ExecuteSQL("EXEC sp_executesql @stmt = N'SELECT * FROM dyn_products WHERE price < @maxPrice', @params = N'@maxPrice INT', @maxPrice = 10;")
	if err != nil {
		t.Fatalf("sp_executesql named @stmt/@params failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "Pen" {
		t.Fatalf("unexpected result: %+v", res.Rows)
	}

	// 3. Positional values (without @name = val)
	res, err = s.ExecuteSQL("EXEC sp_executesql N'SELECT * FROM dyn_products WHERE id = @id AND price > @minPrice', N'@id INT, @minPrice INT', 1, 10;")
	if err != nil {
		t.Fatalf("sp_executesql positional values failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "Book" {
		t.Fatalf("unexpected result: %+v", res.Rows)
	}

	// 4. Session variables passed as @stmt and @params
	sessID := "sp_sess"
	_, err = s.ExecuteSessionSQL(sessID, "DECLARE @q NVARCHAR(200) = 'SELECT * FROM dyn_products WHERE id = @pId';")
	if err != nil {
		t.Fatalf("DECLARE @q failed: %v", err)
	}
	_, err = s.ExecuteSessionSQL(sessID, "DECLARE @p NVARCHAR(50) = '@pId INT';")
	if err != nil {
		t.Fatalf("DECLARE @p failed: %v", err)
	}
	res, err = s.ExecuteSessionSQL(sessID, "EXEC sp_executesql @q, @p, @pId = 2;")
	if err != nil {
		t.Fatalf("sp_executesql with session var stmt/params failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "Pen" {
		t.Fatalf("unexpected result from session var sp_executesql: %+v", res.Rows)
	}

	// 5. Parameter name inside string literal MUST NOT be substituted
	res, err = s.ExecuteSQL("EXEC sp_executesql N'SELECT @val AS id, ''@val inside literal'' AS note', N'@val INT', @val = 99;")
	if err != nil {
		t.Fatalf("sp_executesql with literal protection failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["note"] != "@val inside literal" {
		t.Fatalf("literal parameter was wrongly substituted: %+v", res.Rows)
	}

	// 6. OUTPUT keyword syntax compatibility
	res, err = s.ExecuteSQL("EXEC sp_executesql N'SELECT * FROM dyn_products WHERE id = @id', N'@id INT OUTPUT', @id = 1 OUTPUT;")
	if err != nil {
		t.Fatalf("sp_executesql with OUTPUT failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["title"] != "Book" {
		t.Fatalf("unexpected result from OUTPUT syntax: %+v", res.Rows)
	}
}

func TestDynamicSQL_StringConcatenation(t *testing.T) {
	s, cleanup := setupTestServer()
	defer cleanup()

	_, err := s.ExecuteSQL("CREATE TABLE dyn_concat (id INT PRIMARY KEY, msg VARCHAR(50));")
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	_, err = s.ExecuteSQL("INSERT INTO dyn_concat VALUES (1, 'Hello World');")
	if err != nil {
		t.Fatalf("failed to insert data: %v", err)
	}

	// 1. Literal concatenation: EXEC ('SELECT ' + '* FROM dyn_concat')
	res, err := s.ExecuteSQL("EXEC ('SELECT ' + '* FROM dyn_concat WHERE id = 1');")
	if err != nil {
		t.Fatalf("EXEC with literal concatenation failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["msg"] != "Hello World" {
		t.Fatalf("unexpected result: %+v", res.Rows)
	}

	// 2. Variable concatenation in session
	sessID := "concat_sess"
	_, err = s.ExecuteSessionSQL(sessID, "DECLARE @tbl VARCHAR(50) = 'dyn_concat';")
	if err != nil {
		t.Fatalf("DECLARE @tbl failed: %v", err)
	}
	res, err = s.ExecuteSessionSQL(sessID, "EXEC ('SELECT msg FROM ' + @tbl + ' WHERE id = 1');")
	if err != nil {
		t.Fatalf("EXEC with variable concatenation failed: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0]["msg"] != "Hello World" {
		t.Fatalf("unexpected result: %+v", res.Rows)
	}
}

func TestDynamicSQL_DMLAndDDL(t *testing.T) {
	s, cleanup := setupTestServer()
	defer cleanup()

	// 1. Dynamic DDL: CREATE TABLE via EXEC
	_, err := s.ExecuteSQL("EXEC ('CREATE TABLE dyn_ddl_test (id INT PRIMARY KEY, val VARCHAR(20));');")
	if err != nil {
		t.Fatalf("dynamic CREATE TABLE failed: %v", err)
	}

	// 2. Dynamic DML: INSERT via sp_executesql
	res, err := s.ExecuteSQL("EXEC sp_executesql N'INSERT INTO dyn_ddl_test (id, val) VALUES (@id, @val)', N'@id INT, @val VARCHAR(20)', @id = 100, @val = 'created_dynamic';")
	if err != nil {
		t.Fatalf("dynamic INSERT failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected rows affected = 1, got %d", res.RowsAffected)
	}

	// 3. Dynamic DML: UPDATE via sp_executesql
	res, err = s.ExecuteSQL("EXEC sp_executesql N'UPDATE dyn_ddl_test SET val = @newVal WHERE id = @id', N'@newVal VARCHAR(20), @id INT', @newVal = 'updated_dynamic', @id = 100;")
	if err != nil {
		t.Fatalf("dynamic UPDATE failed: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("expected rows affected = 1, got %d", res.RowsAffected)
	}

	// 4. Verify updated value
	selRes, err := s.ExecuteSQL("SELECT val FROM dyn_ddl_test WHERE id = 100;")
	if err != nil || len(selRes.Rows) != 1 || selRes.Rows[0]["val"] != "updated_dynamic" {
		t.Fatalf("unexpected value after dynamic update: %+v", selRes.Rows)
	}

	// 5. Error handling: invalid query inside EXEC
	_, err = s.ExecuteSQL("EXEC ('INVALID SYNTAX STATEMENT');")
	if err == nil {
		t.Fatalf("expected error for invalid dynamic SQL, got nil")
	}

	// 6. Error handling: missing @stmt in sp_executesql
	_, err = s.ExecuteSQL("EXEC sp_executesql;")
	if err == nil {
		t.Fatalf("expected error for empty sp_executesql, got nil")
	}
}


