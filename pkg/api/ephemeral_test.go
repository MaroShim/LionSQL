package api_test

import (
	"fmt"
	"testing"

	"sqlion/pkg/api"
	"sqlion/pkg/catalog"
	"sqlion/pkg/storage/memengine"
)

func TestDBCC_PersistenceAndEphemeral(t *testing.T) {
	// 1. Setup server with ephemeral memory engine
	eng := memengine.New()
	_ = eng.OpenWithOptions(memengine.Options{
		DataDir:   "",
		EnableAOF: false,
		EnableRDB: false,
	})
	cat := catalog.New(eng)
	server := api.NewServer(cat, eng)
	defer server.Close()

	// 2. Execute DBCC PERSISTENCE
	res, err := server.ExecuteSQL("DBCC PERSISTENCE;")
	if err != nil {
		t.Fatalf("DBCC PERSISTENCE failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected Success=true, got: %+v", res)
	}

	foundEngine := false
	foundPersistence := false
	for _, row := range res.Rows {
		prop := fmt.Sprintf("%v", row["Property"])
		val := fmt.Sprintf("%v", row["Value"])
		if prop == "Engine" && val == "In-Memory OCC" {
			foundEngine = true
		}
		if prop == "Persistence" && val == "ephemeral" {
			foundPersistence = true
		}
	}
	if !foundEngine || !foundPersistence {
		t.Fatalf("expected Engine='In-Memory OCC' and Persistence='ephemeral', got rows: %+v", res.Rows)
	}

	// 3. Execute DBCC USEROPTIONS
	res, err = server.ExecuteSQL("DBCC USEROPTIONS;")
	if err != nil {
		t.Fatalf("DBCC USEROPTIONS failed: %v", err)
	}
	hasPersistenceOpt := false
	for _, row := range res.Rows {
		if fmt.Sprintf("%v", row["Set Option"]) == "persistence" && fmt.Sprintf("%v", row["Value"]) == "ephemeral" {
			hasPersistenceOpt = true
			break
		}
	}
	if !hasPersistenceOpt {
		t.Fatalf("expected DBCC USEROPTIONS to contain persistence='ephemeral', got: %+v", res.Rows)
	}
}
