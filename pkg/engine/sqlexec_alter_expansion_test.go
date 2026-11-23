package engine

import (
	"context"
	"strings"
	"testing"
)

func TestEngine_AlterColumnOnlineExpansion(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	ctx := context.Background()
	sessID := "sess_alter_expansion"
	_, err := coord.GetOrCreateSession(sessID)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// 1. Create table with VARCHAR(10)
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `CREATE TABLE t_online_exp (id INT PRIMARY KEY, title VARCHAR(10));`)
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `INSERT INTO t_online_exp VALUES (1, 'short');`)
	if err != nil {
		t.Fatalf("initial INSERT failed: %v", err)
	}

	// Inserting > 10 chars should initially fail due to length 10
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `INSERT INTO t_online_exp VALUES (2, 'this_is_a_very_long_string_exceeding_ten');`)
	if err == nil {
		t.Fatalf("expected insert exceeding 10 chars to fail, but succeeded")
	}

	// 2. Online Expand to VARCHAR(50) (T-SQL syntax: ALTER TABLE ... ALTER COLUMN ... <type>)
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `ALTER TABLE t_online_exp ALTER COLUMN title VARCHAR(50);`)
	if err != nil {
		t.Fatalf("ALTER COLUMN expansion failed: %v", err)
	}

	// 3. Inserting > 10 chars should now succeed
	longStr := "this_is_a_very_long_string_exceeding_ten"
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `INSERT INTO t_online_exp VALUES (2, '`+longStr+`');`)
	if err != nil {
		t.Fatalf("INSERT after expansion failed: %v", err)
	}

	res, err := coord.ExecuteSessionSQLContext(ctx, sessID, `SELECT id, title FROM t_online_exp ORDER BY id;`)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[1]["title"] != longStr {
		t.Fatalf("expected %q, got %v", longStr, res.Rows[1]["title"])
	}

	// 4. Shrinking to VARCHAR(15) should fail closed because row 2 has 40 chars
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `ALTER TABLE t_online_exp ALTER COLUMN title VARCHAR(15);`)
	if err == nil {
		t.Fatalf("expected shrinking below max data length to fail, but succeeded")
	}
	if !strings.Contains(err.Error(), "8152") && !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("expected truncation error Msg 8152, got: %v", err)
	}

	// 5. PostgreSQL syntax: ALTER TABLE ... ALTER COLUMN ... TYPE VARCHAR(100)
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `ALTER TABLE t_online_exp ALTER COLUMN title TYPE VARCHAR(100);`)
	if err != nil {
		t.Fatalf("PG style ALTER COLUMN TYPE failed: %v", err)
	}

	// 6. PostgreSQL syntax: ALTER TABLE ... ALTER COLUMN ... SET DATA TYPE VARCHAR(120)
	_, err = coord.ExecuteSessionSQLContext(ctx, sessID, `ALTER TABLE t_online_exp ALTER COLUMN title SET DATA TYPE VARCHAR(120);`)
	if err != nil {
		t.Fatalf("PG style ALTER COLUMN SET DATA TYPE failed: %v", err)
	}
}
