package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/cli"
	"lionsql/pkg/engine"
	"lionsql/pkg/storage/memengine"
)

func setupTestCoordinator(t *testing.T) (*engine.Coordinator, func()) {
	t.Helper()
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	return coord, func() {
		coord.Close()
		eng.Close()
	}
}

func TestCLI_ExecuteFlag(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer

	cfg := cli.Config{
		Execute: "SELECT 42 AS answer, 'LionSQL' AS engine_name;",
		Out:     &outBuf,
		Err:     &errBuf,
	}

	err := cli.RunCLI(cfg, coord)
	if err != nil {
		t.Fatalf("RunCLI failed: %v", err)
	}

	out := outBuf.String()
	if !strings.Contains(out, "answer") || !strings.Contains(out, "42") || !strings.Contains(out, "LionSQL") {
		t.Fatalf("expected output to contain query results, got: %s", out)
	}
}

func TestCLI_FileFlag(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	tmpDir := t.TempDir()
	sqlFile := filepath.Join(tmpDir, "script.sql")
	scriptContent := `
CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(50));
INSERT INTO users (id, name) VALUES (1, 'Alice'), (2, 'Bob');
SELECT name FROM users ORDER BY id;
`
	if err := os.WriteFile(sqlFile, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("failed to write temp script: %v", err)
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer

	cfg := cli.Config{
		File: sqlFile,
		Out:  &outBuf,
		Err:  &errBuf,
	}

	err := cli.RunCLI(cfg, coord)
	if err != nil {
		t.Fatalf("RunCLI with file failed: %v", err)
	}

	out := outBuf.String()
	if !strings.Contains(out, "Alice") || !strings.Contains(out, "Bob") {
		t.Fatalf("expected script output to contain Alice and Bob, got: %s", out)
	}
}

func TestCLI_Interactive_Exit(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	input := "SELECT 100 AS num;\nexit\n"
	inReader := strings.NewReader(input)
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer

	cfg := cli.Config{
		Interactive: true,
		In:          inReader,
		Out:         &outBuf,
		Err:         &errBuf,
	}

	err := cli.RunCLI(cfg, coord)
	if err != nil {
		t.Fatalf("RunCLI interactive failed: %v", err)
	}

	out := outBuf.String()
	if !strings.Contains(out, "LionSQL Interactive Shell") {
		t.Fatalf("expected banner in interactive output, got: %s", out)
	}
	if !strings.Contains(out, "100") {
		t.Fatalf("expected query result in interactive output, got: %s", out)
	}
}

func TestCLI_ExecuteError(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer

	cfg := cli.Config{
		Execute: "SELECT * FROM missing_table_12345;",
		Out:     &outBuf,
		Err:     &errBuf,
	}

	err := cli.RunCLI(cfg, coord)
	if err == nil {
		t.Fatalf("expected error on query failure, got nil")
	}
	if !strings.Contains(err.Error(), "table not found") && !strings.Contains(err.Error(), "missing_table_12345") {
		t.Fatalf("expected error message to mention table not found, got: %v", err)
	}
}
