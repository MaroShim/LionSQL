package memengine_test

import (
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestEphemeralEngine_ZeroDiskArtifacts(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lionsql_ephemeral_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	eng := memengine.New()
	opts := memengine.Options{
		DataDir:   tempDir,
		EnableAOF: false,
		EnableRDB: false,
	}

	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("OpenWithOptions failed: %v", err)
	}

	if !eng.IsEphemeral() {
		t.Fatal("expected engine to be in ephemeral mode")
	}
	if eng.PersistenceSummary() != "ephemeral" {
		t.Fatalf("expected PersistenceSummary to be 'ephemeral', got: %s", eng.PersistenceSummary())
	}

	// 1. Create table
	schema := &storage.TableSchema{
		Name: "transient_data",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 2. Insert records within transactions
	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if err := tx.Insert("transient_data", storage.Record{"id": storage.IntValue(1), "val": storage.StringValue("hello")}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 3. Close engine
	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 4. Verify ZERO persistence artifacts exist on disk
	walPath := filepath.Join(tempDir, "wal.aof")
	if _, err := os.Stat(walPath); !os.IsNotExist(err) {
		t.Fatalf("expected wal.aof to NOT exist in ephemeral mode, but found: %s", walPath)
	}

	rdbPath := filepath.Join(tempDir, "tables.mpk")
	if _, err := os.Stat(rdbPath); !os.IsNotExist(err) {
		t.Fatalf("expected tables.mpk to NOT exist in ephemeral mode, but found: %s", rdbPath)
	}
}

func TestEphemeralEngine_PureMemoryNoDir(t *testing.T) {
	eng := memengine.New()
	opts := memengine.Options{
		DataDir:   "",
		EnableAOF: false,
		EnableRDB: false,
	}

	if err := eng.OpenWithOptions(opts); err != nil {
		t.Fatalf("OpenWithOptions with empty DataDir failed: %v", err)
	}

	if !eng.IsEphemeral() {
		t.Fatal("expected engine to be ephemeral")
	}

	schema := &storage.TableSchema{
		Name: "cache",
		Columns: []storage.Column{
			{Name: "key", Type: storage.TypeVarChar, PrimaryKey: true},
			{Name: "val", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	if err := eng.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}
