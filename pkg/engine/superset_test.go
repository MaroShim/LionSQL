package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestEngine_UnifiedSuperset_ILikeAndCollate(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	defer coord.Close()

	// 1. Setup table and data
	if _, err := coord.ExecuteSQL(`CREATE TABLE users_superset (id INT PRIMARY KEY, name VARCHAR(50), email VARCHAR(50));`); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`INSERT INTO users_superset VALUES (1, 'Alice', 'Alice@gmail.com'), (2, 'alice', 'alice@company.com'), (3, 'BOB', 'bob@GMAIL.COM');`); err != nil {
		t.Fatalf("INSERT VALUES failed: %v", err)
	}

	// 2. ILIKE test: case-insensitive pattern matching across uppercase/lowercase
	resILike, err := coord.ExecuteSQL(`SELECT id, email FROM users_superset WHERE email ILIKE '%@gmail.com' ORDER BY id ASC;`)
	if err != nil {
		t.Fatalf("SELECT with ILIKE failed: %v", err)
	}
	if len(resILike.Rows) != 2 {
		t.Fatalf("expected 2 rows for email ILIKE '%%@gmail.com', got %d: %+v", len(resILike.Rows), resILike.Rows)
	}

	// 3. Default LIKE test: SQL Server default CI matches both 'Alice' and 'alice'
	resLike, err := coord.ExecuteSQL(`SELECT id, name FROM users_superset WHERE name LIKE 'alice' ORDER BY id ASC;`)
	if err != nil {
		t.Fatalf("SELECT with LIKE failed: %v", err)
	}
	if len(resLike.Rows) != 2 {
		t.Fatalf("expected 2 rows for name LIKE 'alice' (CI default), got %d: %+v", len(resLike.Rows), resLike.Rows)
	}

	// 4. COLLATE in LIKE: Case-Sensitive (CS) matches only 'Alice'
	resCollateLike, err := coord.ExecuteSQL(`SELECT id, name FROM users_superset WHERE name COLLATE Latin1_General_CS_AS LIKE 'Alice%' ORDER BY id ASC;`)
	if err != nil {
		t.Fatalf("SELECT with COLLATE LIKE failed: %v", err)
	}
	if len(resCollateLike.Rows) != 1 || resCollateLike.Rows[0]["name"] != "Alice" {
		t.Fatalf("expected 1 row ('Alice') for COLLATE Latin1_General_CS_AS LIKE 'Alice%%', got: %+v", resCollateLike.Rows)
	}

	// 5. COLLATE in Equality (=): Binary matches only 'Alice'
	resCollateEq, err := coord.ExecuteSQL(`SELECT id, name FROM users_superset WHERE name COLLATE Latin1_General_BIN = 'Alice' ORDER BY id ASC;`)
	if err != nil {
		t.Fatalf("SELECT with COLLATE = failed: %v", err)
	}
	if len(resCollateEq.Rows) != 1 || resCollateEq.Rows[0]["name"] != "Alice" {
		t.Fatalf("expected 1 row ('Alice') for COLLATE Latin1_General_BIN = 'Alice', got: %+v", resCollateEq.Rows)
	}

	// 6. Stored procedure with ILIKE and COLLATE
	procSQL := `
	CREATE PROCEDURE sp_FilterSupersetUsers AS
	BEGIN
		SELECT id, name, email FROM users_superset WHERE email ILIKE '%@gmail.com' AND name COLLATE Latin1_General_CS_AS LIKE 'Alice%' ORDER BY id;
	END;
	`
	if _, err := coord.ExecuteSQL(procSQL); err != nil {
		t.Fatalf("CREATE PROCEDURE failed: %v", err)
	}

	resProc, err := coord.ExecuteSQL(`EXEC sp_FilterSupersetUsers;`)
	if err != nil {
		t.Fatalf("EXEC sp_FilterSupersetUsers failed: %v", err)
	}
	if len(resProc.Rows) != 1 || resProc.Rows[0]["name"] != "Alice" {
		t.Fatalf("expected 1 row ('Alice') from sp_FilterSupersetUsers, got: %+v", resProc.Rows)
	}
}
