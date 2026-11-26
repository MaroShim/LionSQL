//go:build cgo && wiredtiger

package wiredtiger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/storage"
)

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

	// 2. Corrupt catalog.json with a non-existent versioned physical_name (simulating migration artifact)
	catPath := filepath.Join(dir, "catalog.json")
	data, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatalf("ReadFile catalog.json failed: %v", err)
	}
	var tables []*storage.TableSchema
	if err := json.Unmarshal(data, &tables); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Set physical_name to a fake versioned name
	fakePhys := "users_v9999999999999999999"
	tables[0].PhysicalName = fakePhys
	corrupted, _ := json.MarshalIndent(tables, "", "  ")
	if err := os.WriteFile(catPath, corrupted, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Also add a table whose files are completely missing (e.g. data_source)
	missingTbl := &storage.TableSchema{
		Name:         "data_source",
		PhysicalName: "data_source_v8888888888888888888",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "url", Type: storage.TypeVarChar},
		},
	}
	tables = append(tables, missingTbl)
	corruptedWithMissing, _ := json.MarshalIndent(tables, "", "  ")
	if err := os.WriteFile(catPath, corruptedWithMissing, 0644); err != nil {
		t.Fatalf("WriteFile with missing failed: %v", err)
	}

	// 3. Re-open engine: Self-Healing should kick in during loadCatalog()
	eng2 := New()
	if err := eng2.Open(dir); err != nil {
		t.Fatalf("eng2.Open failed with self-healing: %v", err)
	}
	defer eng2.Close()

	// 4. Verify catalog in memory was self-healed
	tblUsers, err := eng2.GetTable("users")
	if err != nil {
		t.Fatalf("GetTable users failed: %v", err)
	}
	if tblUsers.PhysicalName != "users" {
		t.Errorf("expected users.PhysicalName to be healed to 'users', got %q", tblUsers.PhysicalName)
	}

	tblDS, err := eng2.GetTable("data_source")
	if err != nil {
		t.Fatalf("GetTable data_source failed: %v", err)
	}
	if tblDS.PhysicalName != "data_source" {
		t.Errorf("expected data_source.PhysicalName to be healed to 'data_source', got %q", tblDS.PhysicalName)
	}

	// Verify catalog.json on disk was also updated
	reloadedData, _ := os.ReadFile(catPath)
	var reloadedTables []*storage.TableSchema
	_ = json.Unmarshal(reloadedData, &reloadedTables)
	for _, rt := range reloadedTables {
		if rt.Name == "users" && rt.PhysicalName != "users" {
			t.Errorf("catalog.json users.PhysicalName not healed on disk: %q", rt.PhysicalName)
		}
		if rt.Name == "data_source" && rt.PhysicalName != "data_source" {
			t.Errorf("catalog.json data_source.PhysicalName not healed on disk: %q", rt.PhysicalName)
		}
	}
}
