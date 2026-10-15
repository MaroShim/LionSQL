package migrate

import (
	"context"
	"os"
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/engine"
	"sqlion/pkg/storage/memengine"
)

func setupTestCoordinator(t *testing.T) engine.SQLCoordinator {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	return coord
}

func TestMigrate_CreateAndLoad(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create first migration
	m1, err := Create(tempDir, "create-users-table")
	if err != nil {
		t.Fatalf("Create m1 failed: %v", err)
	}
	if m1.Name != "create_users_table" {
		t.Fatalf("Expected sanitized name create_users_table, got %s", m1.Name)
	}

	// 2. Create second migration
	m2, err := Create(tempDir, "add_orders_table")
	if err != nil {
		t.Fatalf("Create m2 failed: %v", err)
	}
	if m2.Name != "add_orders_table" {
		t.Fatalf("Expected sanitized name add_orders_table, got %s", m2.Name)
	}

	// 3. Load migrations
	files, err := Load(tempDir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("Expected 2 migration pairs, got %d", len(files))
	}
	if files[0].Version > files[1].Version {
		t.Fatalf("Expected chronological ordering by version")
	}
}

func TestMigrate_UpAndDownLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	coord := setupTestCoordinator(t)
	ctx := context.Background()

	// Create 2 test migrations
	m1, err := Create(tempDir, "create_customers")
	if err != nil {
		t.Fatalf("Create m1 failed: %v", err)
	}
	up1 := `CREATE TABLE customers (id INT PRIMARY KEY, name VARCHAR(100));
	INSERT INTO customers VALUES (1, 'Alice');`
	down1 := `DROP TABLE customers;`
	_ = os.WriteFile(m1.UpPath, []byte(up1), 0644)
	_ = os.WriteFile(m1.DownPath, []byte(down1), 0644)

	m2, err := Create(tempDir, "create_accounts")
	if err != nil {
		t.Fatalf("Create m2 failed: %v", err)
	}
	up2 := `CREATE TABLE accounts (id INT PRIMARY KEY, balance FLOAT);`
	down2 := `DROP TABLE accounts;`
	_ = os.WriteFile(m2.UpPath, []byte(up2), 0644)
	_ = os.WriteFile(m2.DownPath, []byte(down2), 0644)

	// Check initial status: 2 pending
	statuses, err := Status(ctx, coord, tempDir)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if len(statuses) != 2 || statuses[0].Applied || statuses[1].Applied {
		t.Fatalf("Expected 2 unapplied migrations, got: %+v", statuses)
	}

	// Apply 1 step Up
	applied1, err := Up(ctx, coord, tempDir, 1)
	if err != nil {
		t.Fatalf("Up step 1 failed: %v", err)
	}
	if len(applied1) != 1 || applied1[0].Version != m1.Version {
		t.Fatalf("Expected m1 applied in step 1, got: %+v", applied1)
	}

	// Verify customers table exists and has Alice
	res, err := coord.ExecuteSQL("SELECT name FROM customers WHERE id = 1;")
	if err != nil || len(res.Rows) != 1 || res.Rows[0]["name"] != "Alice" {
		t.Fatalf("Expected customers query to return Alice, got res=%v, err=%v", res, err)
	}

	// Apply remaining step Up (all pending)
	applied2, err := Up(ctx, coord, tempDir, 0)
	if err != nil {
		t.Fatalf("Up all remaining failed: %v", err)
	}
	if len(applied2) != 1 || applied2[0].Version != m2.Version {
		t.Fatalf("Expected m2 applied, got: %+v", applied2)
	}

	// Check status: all applied
	statuses, err = Status(ctx, coord, tempDir)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !statuses[0].Applied || !statuses[1].Applied {
		t.Fatalf("Expected both migrations applied, got: %+v", statuses)
	}

	// Rollback 1 step Down (should rollback accounts)
	reverted1, err := Down(ctx, coord, tempDir, 1)
	if err != nil {
		t.Fatalf("Down step 1 failed: %v", err)
	}
	if len(reverted1) != 1 || reverted1[0].Version != m2.Version {
		t.Fatalf("Expected m2 reverted, got: %+v", reverted1)
	}

	// Verify accounts table dropped, customers table still intact
	_, err = coord.ExecuteSQL("SELECT * FROM accounts;")
	if err == nil {
		t.Fatalf("Expected error querying dropped accounts table")
	}
	res, err = coord.ExecuteSQL("SELECT name FROM customers WHERE id = 1;")
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("Expected customers table still present, got %v", err)
	}

	// Rollback remaining step Down (customers)
	reverted2, err := Down(ctx, coord, tempDir, 1)
	if err != nil {
		t.Fatalf("Down step 2 failed: %v", err)
	}
	if len(reverted2) != 1 || reverted2[0].Version != m1.Version {
		t.Fatalf("Expected m1 reverted, got: %+v", reverted2)
	}

	// Verify customers table dropped
	_, err = coord.ExecuteSQL("SELECT * FROM customers;")
	if err == nil {
		t.Fatalf("Expected error querying dropped customers table")
	}

	// Final status: all pending again
	statuses, err = Status(ctx, coord, tempDir)
	if err != nil {
		t.Fatalf("Final status failed: %v", err)
	}
	if statuses[0].Applied || statuses[1].Applied {
		t.Fatalf("Expected all unapplied, got: %+v", statuses)
	}
}

func TestMigrate_ErrorHandling(t *testing.T) {
	tempDir := t.TempDir()
	coord := setupTestCoordinator(t)
	ctx := context.Background()

	mBad, err := Create(tempDir, "invalid_syntax")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	_ = os.WriteFile(mBad.UpPath, []byte("THIS IS NOT VALID SQL STATEMENT;"), 0644)

	// Up must fail
	applied, err := Up(ctx, coord, tempDir, 0)
	if err == nil {
		t.Fatalf("Expected error from invalid migration SQL, got applied: %+v", applied)
	}

	// _migrations must not record failed migration
	recs, err := GetAppliedMigrations(ctx, coord)
	if err != nil {
		t.Fatalf("GetAppliedMigrations failed: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("Expected 0 applied migrations after error, got %d", len(recs))
	}
}
