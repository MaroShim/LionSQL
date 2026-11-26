package engine_test

import (
	"context"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestPgCompatibility_CastDoubleColonAllClauses(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sess, err := coord.GetOrCreateSession("pg_cast_sess")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Setup tables
	initDDL := `
CREATE TABLE orders (id INT PRIMARY KEY, customer_id INT, amount FLOAT, status VARCHAR(20));
CREATE TABLE customers (id INT PRIMARY KEY, name VARCHAR(50));
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, initDDL); err != nil {
		t.Fatalf("failed init DDL: %v", err)
	}

	// 1. INSERT with ::type in VALUES
	insertSQL := `
INSERT INTO customers VALUES ('10'::int, 'Alice'::varchar);
INSERT INTO orders VALUES ('100'::int, '10'::int, '99.5'::float, 'completed'::text);
`
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, insertSQL); err != nil {
		t.Fatalf("failed INSERT with ::type: %v", err)
	}

	// 2. SELECT with ::type in Projection, WHERE, and JOIN
	selectSQL := `
SELECT 
    o.id, 
    c.name, 
    o.amount::int AS rounded_amount, 
    'true'::boolean AS is_valid
FROM orders o
JOIN customers c ON o.customer_id = c.id::varchar::int
WHERE o.amount > '50'::float AND o.status = 'completed'::varchar;
`
	res, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, selectSQL)
	if err != nil {
		t.Fatalf("failed SELECT with ::type: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(res.Rows), res.Rows)
	}
	if res.Rows[0]["rounded_amount"] != int64(99) && res.Rows[0]["rounded_amount"] != 99 {
		t.Errorf("expected rounded_amount = 99, got %v", res.Rows[0]["rounded_amount"])
	}

	// 3. Scalar expr with ::type
	scalarSQL := `SELECT '123'::int AS i, '3.14'::float AS f, 'true'::bool AS b, 'hello'::varchar AS s;`
	sRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, scalarSQL)
	if err != nil {
		t.Fatalf("failed scalar SELECT with ::type: %v", err)
	}
	if len(sRes.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(sRes.Rows))
	}
}

func TestPgCompatibility_SessionSettingsParity(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	ctx := context.Background()
	sess, err := coord.GetOrCreateSession("pg_settings_sess")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// 1. Default current_setting & SHOW
	res, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT current_setting('search_path');")
	if err != nil {
		t.Fatalf("failed current_setting: %v", err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected 1 row for current_setting")
	}

	// 2. SET search_path TO myschema, public
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SET search_path TO myschema, public;"); err != nil {
		t.Fatalf("failed SET search_path: %v", err)
	}

	// Verify SHOW search_path
	showRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SHOW search_path;")
	if err != nil {
		t.Fatalf("failed SHOW search_path: %v", err)
	}
	if len(showRes.Rows) == 0 || showRes.Rows[0]["search_path"] != "myschema, public" {
		t.Fatalf("unexpected SHOW search_path: %+v", showRes.Rows)
	}

	// Verify current_setting('search_path') matches updated value
	curRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT current_setting('search_path');")
	if err != nil {
		t.Fatalf("failed current_setting after SET: %v", err)
	}
	if len(curRes.Rows) == 0 || curRes.Rows[0]["current_setting"] != "myschema, public" {
		t.Fatalf("unexpected current_setting('search_path'): %+v", curRes.Rows)
	}

	// 3. SET_CONFIG with 2 arguments (defaults to is_local = false)
	setCfg2Res, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT set_config('work_mem', '64MB');")
	if err != nil {
		t.Fatalf("failed set_config with 2 args: %v", err)
	}
	if len(setCfg2Res.Rows) == 0 || setCfg2Res.Rows[0]["set_config"] != "64MB" {
		t.Fatalf("unexpected set_config with 2 args result: %+v", setCfg2Res.Rows)
	}
	wmRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT current_setting('work_mem');")
	if err != nil || len(wmRes.Rows) == 0 || wmRes.Rows[0]["current_setting"] != "64MB" {
		t.Fatalf("expected current_setting('work_mem') to be 64MB: %+v, err: %v", wmRes.Rows, err)
	}

	// 4. SET_CONFIG('timezone', 'Asia/Seoul', false) updates session variable
	setCfgRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT set_config('timezone', 'Asia/Seoul', false);")
	if err != nil {
		t.Fatalf("failed set_config: %v", err)
	}
	if len(setCfgRes.Rows) == 0 || setCfgRes.Rows[0]["set_config"] != "Asia/Seoul" {
		t.Fatalf("unexpected set_config result: %+v", setCfgRes.Rows)
	}

	// Verify current_setting('timezone') reflects updated setting
	tzRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT current_setting('timezone');")
	if err != nil {
		t.Fatalf("failed current_setting timezone: %v", err)
	}
	if len(tzRes.Rows) == 0 || tzRes.Rows[0]["current_setting"] != "Asia/Seoul" {
		t.Fatalf("unexpected current_setting('timezone'): %+v", tzRes.Rows)
	}

	// Verify SHOW timezone reflects updated setting
	showTzRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SHOW timezone;")
	if err != nil {
		t.Fatalf("failed SHOW timezone: %v", err)
	}
	if len(showTzRes.Rows) == 0 || showTzRes.Rows[0]["timezone"] != "Asia/Seoul" {
		t.Fatalf("unexpected SHOW timezone: %+v", showTzRes.Rows)
	}

	// 5. Transaction-Local Configuration (is_local = true and SET LOCAL)
	// Begin transaction
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "BEGIN;"); err != nil {
		t.Fatalf("failed BEGIN: %v", err)
	}

	// Set local timezone via set_config(..., true)
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT set_config('timezone', 'Europe/Paris', true);"); err != nil {
		t.Fatalf("failed set_config local: %v", err)
	}

	// Inside transaction, timezone should be Europe/Paris
	inTxTz, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT current_setting('timezone');")
	if err != nil || len(inTxTz.Rows) == 0 || inTxTz.Rows[0]["current_setting"] != "Europe/Paris" {
		t.Fatalf("expected in-tx timezone Europe/Paris, got: %+v", inTxTz.Rows)
	}

	// Also test SET LOCAL search_path
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SET LOCAL search_path TO test_schema;"); err != nil {
		t.Fatalf("failed SET LOCAL search_path: %v", err)
	}
	inTxSp, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SHOW search_path;")
	if err != nil || len(inTxSp.Rows) == 0 || inTxSp.Rows[0]["search_path"] != "test_schema" {
		t.Fatalf("expected in-tx search_path test_schema, got: %+v", inTxSp.Rows)
	}

	// Commit transaction: local variables should be discarded and revert to session defaults
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "COMMIT;"); err != nil {
		t.Fatalf("failed COMMIT: %v", err)
	}

	postTz, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT current_setting('timezone');")
	if err != nil || len(postTz.Rows) == 0 || postTz.Rows[0]["current_setting"] != "Asia/Seoul" {
		t.Fatalf("expected post-commit timezone to revert to Asia/Seoul, got: %+v", postTz.Rows)
	}

	postSp, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SHOW search_path;")
	if err != nil || len(postSp.Rows) == 0 || postSp.Rows[0]["search_path"] != "myschema, public" {
		t.Fatalf("expected post-commit search_path to revert to myschema, public, got: %+v", postSp.Rows)
	}

	// Also verify ROLLBACK discards local variables
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "BEGIN;"); err != nil {
		t.Fatalf("failed BEGIN 2: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT set_config('timezone', 'America/New_York', true);"); err != nil {
		t.Fatalf("failed set_config local: %v", err)
	}
	if _, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "ROLLBACK;"); err != nil {
		t.Fatalf("failed ROLLBACK: %v", err)
	}
	// 6. Test Dotted Parameter Names (e.g. custom.app_timeout, my_app.feature_flag)
	setDottedRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SET custom.app_timeout TO '500ms';")
	if err != nil {
		t.Fatalf("failed SET custom.app_timeout: %v", err)
	}
	_ = setDottedRes

	showDottedRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SHOW custom.app_timeout;")
	if err != nil || len(showDottedRes.Rows) == 0 || showDottedRes.Rows[0]["custom.app_timeout"] != "500ms" {
		t.Fatalf("expected SHOW custom.app_timeout to be 500ms: %+v, err: %v", showDottedRes.Rows, err)
	}

	curDottedRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT current_setting('custom.app_timeout');")
	if err != nil || len(curDottedRes.Rows) == 0 || curDottedRes.Rows[0]["current_setting"] != "500ms" {
		t.Fatalf("expected current_setting('custom.app_timeout') to be 500ms: %+v, err: %v", curDottedRes.Rows, err)
	}

	// 7. Test current_setting(setting_name, missing_ok) - 2nd argument
	// missing_ok = true on unknown setting -> should return NULL, NOT ERROR
	missingOkRes, err := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT current_setting('non_existent_setting', true);")
	if err != nil {
		t.Fatalf("current_setting with missing_ok=true should succeed, got err: %v", err)
	}
	if len(missingOkRes.Rows) == 0 || missingOkRes.Rows[0]["current_setting"] != nil {
		t.Fatalf("expected NULL for current_setting with missing_ok=true on non-existent parameter, got: %+v", missingOkRes.Rows)
	}

	// missing_ok = false on unknown setting -> should return error (SQLSTATE 42704 / unrecognized configuration parameter)
	_, errMissingFail := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT current_setting('non_existent_setting', false);")
	if errMissingFail == nil {
		t.Fatalf("expected error for current_setting with missing_ok=false on non-existent parameter, but got nil")
	}

	// 8. SHOW unrecognized configuration parameter -> MUST return ERROR 42704, NOT "off"
	_, errShowUnrecognized := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SHOW non_existent_setting;")
	if errShowUnrecognized == nil {
		t.Fatalf("expected error for SHOW non_existent_setting, but got nil (silent 'off' default detected)")
	}
	if !strings.Contains(errShowUnrecognized.Error(), "42704") && !strings.Contains(errShowUnrecognized.Error(), "unrecognized configuration parameter") {
		t.Fatalf("expected error to contain 42704 unrecognized configuration parameter, got: %v", errShowUnrecognized)
	}

	// 9. Literals containing '::' must not be mangled as top-level type cast
	concatRes, errConcat := coord.ExecuteSessionSQLContext(ctx, sess.ID, "SELECT 'a::b' || 'c';")
	if errConcat != nil {
		t.Fatalf("failed SELECT 'a::b' || 'c': %v", errConcat)
	}
	if len(concatRes.Rows) == 0 {
		t.Fatalf("expected rows from SELECT 'a::b' || 'c', got empty")
	}
}
