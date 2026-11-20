package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestEngine_AuditRound12_Regression covers the 5 core root defects identified in Live Audit Round 12:
// - ENG-018: JSON_VALUE & JSON_QUERY strict path mode verification (Msg 13608 on missing keys)
// - ENG-019: JSON_MODIFY "append" modifier on array / scalar
// - ENG-020: Variable method calls (@x.value(), @x.query()) reject silent XML dump
// - ENG-021: CONVERT datetime style validation (Msg 281 on invalid styles)
// - ENG-022: PARSE & TRY_PARSE cultural parse evaluation
func TestEngine_AuditRound12_Regression(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "r12_sess"
	_, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// 1. ENG-018: JSON_VALUE & JSON_QUERY strict path mode
	t.Run("ENG-018_JSON_StrictPathMode", func(t *testing.T) {
		// Lax mode: missing key returns NULL
		resLax, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT JSON_VALUE('{\"a\": 1}', 'lax $.b') AS lax_b;")
		if err != nil {
			t.Fatalf("JSON_VALUE lax failed: %v", err)
		}
		if len(resLax.Rows) == 0 || resLax.Rows[0]["lax_b"] != nil {
			t.Errorf("expected nil for lax missing key, got %v", resLax.Rows)
		}

		// Strict mode: missing key must fail with Msg 13608
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT JSON_VALUE('{\"a\": 1}', 'strict $.b') AS strict_b;")
		if err == nil {
			t.Errorf("expected error for strict missing key in JSON_VALUE, got nil")
		} else if !strings.Contains(err.Error(), "13608") && !strings.Contains(err.Error(), "cannot be found") {
			t.Errorf("expected Msg 13608 error, got: %v", err)
		}

		// Strict mode in JSON_QUERY with scalar target must fail
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT JSON_QUERY('{\"a\": 1}', 'strict $.a') AS strict_q;")
		if err == nil {
			t.Errorf("expected error for strict scalar target in JSON_QUERY, got nil")
		} else if !strings.Contains(err.Error(), "13608") {
			t.Errorf("expected Msg 13608 error, got: %v", err)
		}
	})

	// 2. ENG-019: JSON_MODIFY append modifier
	t.Run("ENG-019_JSON_MODIFY_Append", func(t *testing.T) {
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT JSON_MODIFY('{\"a\": 100}', 'append $.a', 200) AS mod_res;")
		if err != nil {
			t.Fatalf("JSON_MODIFY append failed: %v", err)
		}
		if len(res.Rows) == 0 {
			t.Fatalf("expected 1 row, got 0")
		}
		outStr, ok := res.Rows[0]["mod_res"].(string)
		if !ok {
			t.Fatalf("expected string output, got %T (%v)", res.Rows[0]["mod_res"], res.Rows[0]["mod_res"])
		}
		// Expect "a": [100, 200]
		if !strings.Contains(outStr, `[100,200]`) && !strings.Contains(outStr, `[100, 200]`) {
			t.Errorf("expected array append in JSON_MODIFY, got: %s", outStr)
		}
		if strings.Contains(outStr, "append $") {
			t.Errorf("found corrupted literal 'append $' key in JSON output: %s", outStr)
		}
	})

	// 3. ENG-020: Variable method invocation rejection
	t.Run("ENG-020_Variable_MethodInvocation", func(t *testing.T) {
		_, err := coord.ExecuteSessionSQLContext(ctx, sessID, "DECLARE @x XML = '<root><val>123</val></root>';")
		if err != nil {
			t.Fatalf("DECLARE @x XML failed: %v", err)
		}

		// Method call @x.value(...) must return explicit error (Msg 2812) instead of silent XML dump
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT @x.value('(/root/val)[1]', 'INT') AS val;")
		if err == nil {
			t.Errorf("expected error for unsupported XML method invocation @x.value, got nil")
		} else if !strings.Contains(err.Error(), "2812") && !strings.Contains(err.Error(), "value") {
			t.Errorf("expected Msg 2812 error for @x.value, got: %v", err)
		}
	})

	// 4. ENG-021: CONVERT datetime invalid style rejection
	t.Run("ENG-021_ConvertDateTime_InvalidStyle", func(t *testing.T) {
		// Valid style 112 (YYYYMMDD) should succeed
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CONVERT(VARCHAR, CAST('2026-11-19 12:34:56' AS DATETIME), 112) AS valid_conv;")
		if err != nil {
			t.Fatalf("CONVERT with valid style failed: %v", err)
		}
		if len(res.Rows) == 0 || res.Rows[0]["valid_conv"] != "20261119" {
			t.Errorf("expected '20261119', got %v", res.Rows[0]["valid_conv"])
		}

		// Invalid style 99999 must return Msg 281 error
		_, err = coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT CONVERT(VARCHAR, CAST('2026-11-19 12:34:56' AS DATETIME), 99999) AS invalid_conv;")
		if err == nil {
			t.Errorf("expected error for invalid style 99999, got nil")
		} else if !strings.Contains(err.Error(), "281") && !strings.Contains(err.Error(), "style") {
			t.Errorf("expected Msg 281 error, got: %v", err)
		}
	})

	// 5. ENG-022: PARSE & TRY_PARSE
	t.Run("ENG-022_PARSE_And_TRY_PARSE", func(t *testing.T) {
		res, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT PARSE('123.45' AS DECIMAL(10,2) USING 'en-US') AS num;")
		if err != nil {
			t.Fatalf("PARSE failed: %v", err)
		}
		if len(res.Rows) == 0 {
			t.Fatalf("expected 1 row, got 0")
		}
		valStr := fmt.Sprintf("%v", res.Rows[0]["num"])
		if valStr == "PARSE('123.45' AS DECIMAL(10,2) USING 'en-US')" {
			t.Errorf("PARSE returned raw unparsed expression string instead of parsed value")
		}
		if valStr != "123.45" {
			t.Errorf("expected 123.45 for PARSE, got %v", valStr)
		}

		resTry, err := coord.ExecuteSessionSQLContext(ctx, sessID, "SELECT TRY_PARSE('not_a_number' AS INT) AS num_try;")
		if err != nil {
			t.Fatalf("TRY_PARSE failed: %v", err)
		}
		if len(resTry.Rows) == 0 || resTry.Rows[0]["num_try"] != nil {
			t.Errorf("expected nil for failed TRY_PARSE, got %v", resTry.Rows[0]["num_try"])
		}
	})
}
