//go:build cgo && wiredtiger

package wiredtiger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lionsql/pkg/storage"
)

// TestWiredTiger_CatalogSelfHealing_PhysicalNameReconciliation tests that when a table's physical
// file exists under its canonical Name but catalog has an obsolete PhysicalName timestamp,
// the engine successfully heals PhysicalName -> Name.
func TestWiredTiger_CatalogSelfHealing_PhysicalNameReconciliation(t *testing.T) {
	dir, err := os.MkdirTemp("", "wt_self_healing_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)

	// 1. Initial engine start to create a normal table "users"
	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}

	tblSchema := &storage.TableSchema{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(tblSchema); err != nil {
		eng.Close()
		t.Fatalf("CreateTable failed: %v", err)
	}
	eng.Close()

	// 2. Corrupt catalog.json with a versioned physical_name (simulating migration artifact)
	// while users.wt still genuinely exists on disk.
	catPath := filepath.Join(dir, "catalog.json")
	data, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatalf("ReadFile catalog.json failed: %v", err)
	}
	var tables []*storage.TableSchema
	if err := json.Unmarshal(data, &tables); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	fakePhys := "users_v9999999999999999999"
	tables[0].PhysicalName = fakePhys
	corrupted, _ := json.MarshalIndent(tables, "", "  ")
	if err := os.WriteFile(catPath, corrupted, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// 3. Re-open engine: Self-Healing should kick in and reconcile users.PhysicalName to "users"
	eng2 := New()
	if err := eng2.Open(dir); err != nil {
		t.Fatalf("eng2.Open failed with legitimate rename self-healing: %v", err)
	}
	defer eng2.Close()

	tblUsers, err := eng2.GetTable("users")
	if err != nil {
		t.Fatalf("GetTable users failed: %v", err)
	}
	if tblUsers.PhysicalName != "users" {
		t.Errorf("expected users.PhysicalName to be healed to 'users', got %q", tblUsers.PhysicalName)
	}

	// Verify catalog.json on disk was also updated
	reloadedData, _ := os.ReadFile(catPath)
	var reloadedTables []*storage.TableSchema
	_ = json.Unmarshal(reloadedData, &reloadedTables)
	for _, rt := range reloadedTables {
		if rt.Name == "users" && rt.PhysicalName != "users" {
			t.Errorf("catalog.json users.PhysicalName not healed on disk: %q", rt.PhysicalName)
		}
	}
}

// TestWiredTiger_CatalogIntegrity_DataLossProhibitsAutoRecreate verifies that when a table's physical
// file is genuinely missing from disk, the engine strictly REFUSES to auto-create an empty table,
// immediately failing startup with a descriptive error ([RULE:ExplicitError], Fail-Closed).
func TestWiredTiger_CatalogIntegrity_DataLossProhibitsAutoRecreate(t *testing.T) {
	dir, err := os.MkdirTemp("", "wt_dataloss_check_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)

	// 1. Initial engine start to create a normal table "accounts"
	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}

	tblSchema := &storage.TableSchema{
		Name: "accounts",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "balance", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(tblSchema); err != nil {
		eng.Close()
		t.Fatalf("CreateTable failed: %v", err)
	}
	eng.Close()

	// 2. Simulate catastrophic disk deletion / accidental file loss of accounts.wt
	tableFile := filepath.Join(dir, "accounts.wt")
	if err := os.Remove(tableFile); err != nil {
		t.Fatalf("Remove accounts.wt failed: %v", err)
	}

	// 3. Attempt to re-open engine: MUST FAIL CLOSED!
	eng2 := New()
	openErr := eng2.Open(dir)
	if openErr == nil {
		eng2.Close()
		t.Fatalf("eng2.Open should have failed due to missing table file accounts.wt, but succeeded (silent empty table recreation detected!)")
	}

	if !strings.Contains(openErr.Error(), "physical table file missing for table \"accounts\"") {
		t.Errorf("expected open error to mention missing physical table file for 'accounts', got: %v", openErr)
	}
}

// TestWiredTiger_CatalogIntegrity_IndexLossProhibitsStartup verifies that when a table has secondary
// indexes defined in catalog but the multiplexed index file _idx_<table>.wt is missing, the engine
// fails startup to prevent silent index loss.
func TestWiredTiger_CatalogIntegrity_IndexLossProhibitsStartup(t *testing.T) {
	dir, err := os.MkdirTemp("", "wt_idxloss_check_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)

	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}

	tblSchema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "customer_id", Type: storage.TypeInt},
		},
		Indexes: []storage.IndexSchema{
			{Name: "idx_cust", Columns: []string{"customer_id"}},
		},
	}
	if err := eng.CreateTable(tblSchema); err != nil {
		eng.Close()
		t.Fatalf("CreateTable failed: %v", err)
	}
	eng.Close()

	// Remove index file
	idxFile := filepath.Join(dir, "_idx_orders.wt")
	if err := os.Remove(idxFile); err != nil {
		t.Fatalf("Remove _idx_orders.wt failed: %v", err)
	}

	eng2 := New()
	openErr := eng2.Open(dir)
	if openErr == nil {
		eng2.Close()
		t.Fatalf("eng2.Open should have failed due to missing index file _idx_orders.wt, but succeeded")
	}

	if !strings.Contains(openErr.Error(), "index file missing for table \"orders\"") {
		t.Errorf("expected error to mention missing index file for 'orders', got: %v", openErr)
	}
}

// TestWiredTiger_CatalogIntegrity_LegacyIndexMigrationAllowed verifies that when an older version
// has per-index files (e.g. _idx_orders_idx_cust.wt) and the multiplexed file _idx_orders.wt does not
// yet exist, loadCatalog allows startup to proceed so migrateIndexFiles() can cleanly perform
// the ARCH-WT-FILE-LAYOUT-1 migration without false-positive fail-closed aborts.
func TestWiredTiger_CatalogIntegrity_LegacyIndexMigrationAllowed(t *testing.T) {
	dir, err := os.MkdirTemp("", "wt_legacy_idx_mig_*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)

	eng := New()
	if err := eng.Open(dir); err != nil {
		t.Fatalf("eng.Open failed: %v", err)
	}

	// 1. Create table WITHOUT indexes (no multiplexed file _idx_orders.wt created)
	tblSchema := &storage.TableSchema{
		Name: "orders",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "customer_id", Type: storage.TypeInt},
		},
	}
	if err := eng.CreateTable(tblSchema); err != nil {
		eng.Close()
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 2. Create legacy per-index table in WiredTiger directly (ARCH-WT-FILE-LAYOUT-1 migration test)
	if err := eng.CreateLegacyIndexForTesting("orders", "idx_cust"); err != nil {
		eng.Close()
		t.Fatalf("CreateLegacyIndexForTesting failed: %v", err)
	}

	eng.Close()

	// 3. Update catalog.json to register idx_cust under orders.Indexes (simulating legacy upgrade state)
	catPath := filepath.Join(dir, "catalog.json")
	catData, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatalf("ReadFile catalog.json failed: %v", err)
	}
	var tables []*storage.TableSchema
	if err := json.Unmarshal(catData, &tables); err != nil {
		t.Fatalf("Unmarshal catalog failed: %v", err)
	}
	tables[0].Indexes = []storage.IndexSchema{
		{Name: "idx_cust", TableName: "orders", Columns: []string{"customer_id"}},
	}
	updatedCat, _ := json.MarshalIndent(tables, "", "  ")
	if err := os.WriteFile(catPath, updatedCat, 0644); err != nil {
		t.Fatalf("WriteFile catalog failed: %v", err)
	}

	multiIdxFile := filepath.Join(dir, "_idx_orders.wt")
	legacyIdxFile := filepath.Join(dir, "_idx_orders_idx_cust.wt")

	// Ensure preconditions: legacy file exists, multiplexed file does NOT exist
	if _, err := os.Stat(legacyIdxFile); err != nil {
		t.Fatalf("expected legacy index file %s to exist before restart, got: %v", legacyIdxFile, err)
	}
	if _, err := os.Stat(multiIdxFile); err == nil {
		t.Fatalf("multiplexed index file %s should not exist prior to migration", multiIdxFile)
	}

	// 4. Re-open engine: should succeed, migrate legacy file to multiplexed file, and drop legacy file
	eng2 := New()
	if err := eng2.Open(dir); err != nil {
		t.Fatalf("eng2.Open with legacy index file should have succeeded and migrated, but failed: %v", err)
	}
	defer eng2.Close()

	// Verify multiplexed file was created and legacy file was cleaned up
	if _, err := os.Stat(multiIdxFile); err != nil {
		t.Errorf("expected multiplexed index file %s to exist after migration, got err: %v", multiIdxFile, err)
	}
	if _, err := os.Stat(legacyIdxFile); err == nil {
		t.Errorf("expected legacy index file %s to be dropped after migration, but still exists", legacyIdxFile)
	}
}

