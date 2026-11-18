package engine

import (
	"testing"
)

func TestSessionVar_ArithmeticExpressionEvaluation(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()
	sessID := "test_sess_var_eval"

	// 1. Initial assignment: SET @my_var = 10
	res, err := coord.ExecuteSessionSQL(sessID, "SET @my_var = 10;")
	if err != nil {
		t.Fatalf("SET @my_var = 10 failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("SET command not successful")
	}

	// 2. Arithmetic update: SET @my_var = @my_var + 50
	res, err = coord.ExecuteSessionSQL(sessID, "SET @my_var = @my_var + 50;")
	if err != nil {
		t.Fatalf("SET @my_var = @my_var + 50 failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("SET update command not successful")
	}

	// 3. Query variable: SELECT @my_var
	res, err = coord.ExecuteSessionSQL(sessID, "SELECT @my_var;")
	if err != nil {
		t.Fatalf("SELECT @my_var failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	val := res.Rows[0]["@my_var"]
	intVal, ok := val.(int64)
	if !ok {
		t.Fatalf("expected @my_var to be int64, got %T (%v)", val, val)
	}
	if intVal != 60 {
		t.Fatalf("expected @my_var = 60, got %d", intVal)
	}

	// 4. Repeated update: SET @my_var = @my_var * 2 + 5
	_, err = coord.ExecuteSessionSQL(sessID, "SET @my_var = @my_var * 2 + 5;")
	if err != nil {
		t.Fatalf("SET @my_var compound failed: %v", err)
	}
	res, err = coord.ExecuteSessionSQL(sessID, "SELECT @my_var;")
	if err != nil {
		t.Fatalf("SELECT @my_var failed: %v", err)
	}
	val = res.Rows[0]["@my_var"]
	if v, ok := val.(int64); !ok || v != 125 {
		t.Fatalf("expected @my_var = 125, got %v (%T)", val, val)
	}

	// 5. String concatenation: SET @s = 'hello'; SET @s = @s + ' world';
	_, err = coord.ExecuteSessionSQL(sessID, "SET @s = 'hello';")
	if err != nil {
		t.Fatalf("SET @s = 'hello' failed: %v", err)
	}
	_, err = coord.ExecuteSessionSQL(sessID, "SET @s = @s + ' world';")
	if err != nil {
		t.Fatalf("SET @s = @s + ' world' failed: %v", err)
	}
	res, err = coord.ExecuteSessionSQL(sessID, "SELECT @s;")
	if err != nil {
		t.Fatalf("SELECT @s failed: %v", err)
	}
	if sVal, ok := res.Rows[0]["@s"].(string); !ok || sVal != "hello world" {
		t.Fatalf("expected @s = 'hello world', got %v (%T)", res.Rows[0]["@s"], res.Rows[0]["@s"])
	}

	// 6. DECLARE with arithmetic expression: DECLARE @total INT = 100 + 200;
	_, err = coord.ExecuteSessionSQL(sessID, "DECLARE @total INT = 100 + 200;")
	if err != nil {
		t.Fatalf("DECLARE @total failed: %v", err)
	}
	res, err = coord.ExecuteSessionSQL(sessID, "SELECT @total;")
	if err != nil {
		t.Fatalf("SELECT @total failed: %v", err)
	}
	if tVal, ok := res.Rows[0]["@total"].(int64); !ok || tVal != 300 {
		t.Fatalf("expected @total = 300, got %v (%T)", res.Rows[0]["@total"], res.Rows[0]["@total"])
	}
}
