package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestVaultwarden_CompoundAlterTable(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	ddl := `CREATE TABLE attachments (
		id VARCHAR(50) PRIMARY KEY,
		file_size INT NULL
	);`
	if _, err := coord.ExecuteSQL(ddl); err != nil {
		t.Fatalf("CREATE TABLE attachments failed: %v", err)
	}

	// Compound ALTER TABLE with multiple actions separated by comma
	compoundAlter := `ALTER TABLE attachments ALTER COLUMN file_size TYPE BIGINT, ALTER COLUMN file_size SET NOT NULL;`
	if _, err := coord.ExecuteSQL(compoundAlter); err != nil {
		t.Fatalf("Compound ALTER TABLE failed: %v", err)
	}

	tbl, err := eng.GetTable("attachments")
	if err != nil {
		t.Fatalf("GetTable attachments failed: %v", err)
	}
	col, _, ok := tbl.FindColumn("file_size")
	if !ok || col == nil {
		t.Fatalf("column file_size not found")
	}
	if col.Nullable {
		t.Errorf("expected file_size to be NOT NULL, got nullable")
	}
}

func TestVaultwarden_InsertDefaultKeywordAndCompositePKOps(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	// 1. Devices table with composite PK (uuid, user_uuid)
	ddl := `CREATE TABLE devices (
		uuid VARCHAR(50) NOT NULL,
		user_uuid VARCHAR(50) NOT NULL,
		name VARCHAR(100) NULL,
		created_at TIMESTAMP NULL,
		PRIMARY KEY (uuid, user_uuid)
	);`
	if _, err := coord.ExecuteSQL(ddl); err != nil {
		t.Fatalf("CREATE TABLE devices failed: %v", err)
	}

	// Insert row with DEFAULT keyword for nullable created_at
	insertSQL := `INSERT INTO devices (uuid, user_uuid, name, created_at) VALUES ('dev-1', 'usr-1', 'Desktop', DEFAULT);`
	if _, err := coord.ExecuteSQL(insertSQL); err != nil {
		t.Fatalf("INSERT with DEFAULT keyword failed: %v", err)
	}

	// Verify insertion
	sel, err := coord.ExecuteSQL(`SELECT * FROM devices WHERE uuid = 'dev-1' AND user_uuid = 'usr-1'`)
	if err != nil {
		t.Fatalf("SELECT devices failed: %v", err)
	}
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(sel.Rows))
	}

	// 2. Test ON CONFLICT DO UPDATE on composite primary key
	upsertSQL := `INSERT INTO devices (uuid, user_uuid, name) VALUES ('dev-1', 'usr-1', 'Updated Desktop')
		ON CONFLICT (uuid, user_uuid) DO UPDATE SET name = EXCLUDED.name;`
	if _, err := coord.ExecuteSQL(upsertSQL); err != nil {
		t.Fatalf("ON CONFLICT DO UPDATE on composite PK failed: %v", err)
	}

	sel2, err := coord.ExecuteSQL(`SELECT name FROM devices WHERE uuid = 'dev-1' AND user_uuid = 'usr-1'`)
	if err != nil {
		t.Fatalf("SELECT devices after upsert failed: %v", err)
	}
	if len(sel2.Rows) != 1 || sel2.Rows[0]["name"] != "Updated Desktop" {
		t.Errorf("expected name 'Updated Desktop', got %v", sel2.Rows[0]["name"])
	}
}

func TestVaultwarden_ParenthesizedFromJoin(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	if _, err := coord.ExecuteSQL(`CREATE TABLE users (id VARCHAR(50) PRIMARY KEY, email VARCHAR(100));`); err != nil {
		t.Fatalf("CREATE TABLE users failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`CREATE TABLE ciphers (id VARCHAR(50) PRIMARY KEY, user_id VARCHAR(50), name VARCHAR(100));`); err != nil {
		t.Fatalf("CREATE TABLE ciphers failed: %v", err)
	}

	if _, err := coord.ExecuteSQL(`INSERT INTO users (id, email) VALUES ('u1', 'user@example.com');`); err != nil {
		t.Fatalf("INSERT users failed: %v", err)
	}
	if _, err := coord.ExecuteSQL(`INSERT INTO ciphers (id, user_id, name) VALUES ('c1', 'u1', 'Vault Item');`); err != nil {
		t.Fatalf("INSERT ciphers failed: %v", err)
	}

	// Diesel often generates parenthesized FROM clauses: FROM ("users" INNER JOIN "ciphers" ON ...)
	query := `SELECT users.email, ciphers.name FROM ("users" INNER JOIN "ciphers" ON users.id = ciphers.user_id) WHERE users.id = 'u1';`
	res, err := coord.ExecuteSQL(query)
	if err != nil {
		t.Fatalf("SELECT with parenthesized FROM JOIN failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0]["name"] != "Vault Item" {
		t.Errorf("expected 'Vault Item', got %v", res.Rows[0]["name"])
	}
}
