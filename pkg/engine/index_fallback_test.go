package engine_test

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func TestEngine_DisabledIndexFallbackToTableScan(t *testing.T) {
	dataDir := t.TempDir()
	eng := memengine.New()
	opts := memengine.DefaultOptions(dataDir)
	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer eng.Close()

	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})

	// 1. Create table and index
	_, err := coord.ExecuteSQL("CREATE TABLE accounts (id INT PRIMARY KEY, email VARCHAR(100), balance INT);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
	_, err = coord.ExecuteSQL("CREATE INDEX ix_acc_email ON accounts (email);")
	if err != nil {
		t.Fatalf("CREATE INDEX failed: %v", err)
	}

	// 2. Insert records
	_, err = coord.ExecuteSQL("INSERT INTO accounts VALUES (1, 'user1@example.com', 100), (2, 'user2@example.com', 200);")
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 3. Mark the index disabled in the schema to simulate post-recovery disabled state
	schema, err := eng.GetTable("accounts")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	idx, _, found := schema.FindIndex("ix_acc_email")
	if !found || idx == nil {
		t.Fatalf("FindIndex failed to locate ix_acc_email")
	}
	idx.Disabled = true

	// 4. Verify sys.indexes reports is_disabled = true
	res, err := coord.ExecuteSQL("SELECT name, is_disabled FROM sys.indexes WHERE name = 'ix_acc_email';")
	if err != nil {
		t.Fatalf("sys.indexes query failed: %v", err)
	}
	if len(res.Rows) == 0 {
		t.Fatalf("expected row in sys.indexes, got 0")
	}
	if res.Rows[0]["is_disabled"] != true && res.Rows[0]["is_disabled"] != 1 && res.Rows[0]["is_disabled"] != "true" {
		t.Errorf("expected is_disabled to be true, got: %v", res.Rows[0]["is_disabled"])
	}

	// 5. Execute query on the indexed column: SELECT * FROM accounts WHERE email = 'user1@example.com'
	// Since index is disabled, planner MUST fall back to Table Scan and return user1!
	queryRes, err := coord.ExecuteSQL("SELECT id, email, balance FROM accounts WHERE email = 'user1@example.com';")
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(queryRes.Rows) != 1 {
		t.Fatalf("expected 1 row from Table Scan fallback, got %d", len(queryRes.Rows))
	}
	if queryRes.Rows[0]["email"] != "user1@example.com" {
		t.Errorf("expected user1@example.com, got %v", queryRes.Rows[0]["email"])
	}
}
