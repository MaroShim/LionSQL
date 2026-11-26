package engine_test

import (
	"context"
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

	// 3. SET_CONFIG('timezone', 'Asia/Seoul', false) updates session variable
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
}
