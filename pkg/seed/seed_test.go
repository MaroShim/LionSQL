package seed_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lionsql/pkg/seed"
)

func TestDemoSeedSQL_EmbeddedContentNotEmpty(t *testing.T) {
	if len(seed.DemoSeedSQL) == 0 {
		t.Fatalf("expected embedded seed.DemoSeedSQL to be non-empty")
	}
	if !strings.Contains(seed.DemoSeedSQL, "CREATE TABLE customers") {
		t.Errorf("expected DemoSeedSQL to contain 'CREATE TABLE customers'")
	}
	if !strings.Contains(seed.DemoSeedSQL, "CREATE PROCEDURE TransferFunds") {
		t.Errorf("expected DemoSeedSQL to contain 'CREATE PROCEDURE TransferFunds'")
	}
}

func TestDemoSeedSQL_MatchesScriptsDirectoryFile(t *testing.T) {
	scriptsPath := filepath.Join("..", "..", "scripts", "demo_seed.sql")
	diskBytes, err := os.ReadFile(scriptsPath)
	if err != nil {
		t.Fatalf("failed to read %s: %v", scriptsPath, err)
	}

	if !bytes.Equal([]byte(seed.DemoSeedSQL), diskBytes) {
		t.Fatalf("embedded seed.DemoSeedSQL does not match %s byte-for-byte", scriptsPath)
	}
}

func TestDemoSeedMetadata(t *testing.T) {
	if len(seed.DemoTables) != 5 {
		t.Errorf("expected 5 demo tables, got %d", len(seed.DemoTables))
	}
	if len(seed.DemoProcedures) != 2 {
		t.Errorf("expected 2 demo procedures, got %d", len(seed.DemoProcedures))
	}
	if seed.DemoRecordCount != 34 {
		t.Errorf("expected 34 demo records, got %d", seed.DemoRecordCount)
	}
}
