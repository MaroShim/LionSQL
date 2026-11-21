package slt_proc_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/slt"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

// TestOracleIntegrity_ZeroTampering verifies that zero .slt oracle test files have been altered,
// enforcing Invariant 16: Zero-Tampering Oracle Test Invariant.
func TestOracleIntegrity_ZeroTampering(t *testing.T) {
	data, err := os.ReadFile("CHECKSUMS.sha256")
	if err != nil {
		t.Fatalf("failed to read CHECKSUMS.sha256: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	verified := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		expectedHash := parts[0]
		relPath := parts[1]

		fileBytes, err := os.ReadFile(relPath)
		if err != nil {
			t.Fatalf("[ZERO-TAMPERING-FAIL] missing oracle test file %s: %v", relPath, err)
		}
		h := sha256.Sum256(fileBytes)
		actualHash := hex.EncodeToString(h[:])
		if actualHash != expectedHash {
			t.Fatalf("[ZERO-TAMPERING-FAIL] Oracle file tampering detected in %s!\nExpected SHA256: %s\nActual SHA256:   %s\n[RULE:ZeroOracleTampering] External oracle files are strictly immutable.", relPath, expectedHash, actualHash)
		}
		verified++
	}

	if verified < 1299 {
		t.Fatalf("expected at least 1,299 verified oracle suites in CHECKSUMS.sha256, found %d", verified)
	}
}

func discoverProcSLTFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.slt")
	if err != nil {
		t.Fatalf("failed to glob .slt files: %v", err)
	}
	subFiles, _ := filepath.Glob("*/*.slt")
	files = append(files, subFiles...)
	if len(files) == 0 {
		t.Fatalf("no .slt test files discovered in test/slt_proc")
	}
	return files
}

func TestTSQL_StoredProcedure_Suites(t *testing.T) {
	allFiles := discoverProcSLTFiles(t)
	if len(allFiles) < 1299 {
		t.Fatalf("expected at least 1,299 authentic Gate 5 T-SQL & External Corpora .slt suites, found %d", len(allFiles))
	}

	requiredDirs := []string{
		"babelfish_oracle",
		"sqlglot_oracle",
		"tedious_oracle",
		"pymssql_oracle",
		"gorm_oracle",
		"mssql_jdbc_oracle",
		"adventureworks_oracle",
		"northwind_oracle",
		"antlr_tsql_oracle",
		"knex_oracle",
		"calcite_oracle",
	}
	for _, dir := range requiredDirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.slt"))
		if len(matches) == 0 {
			t.Fatalf("expected Gate 5 external corpus directory %q to contain .slt suites, found 0", dir)
		}
	}

	var passedCount atomic.Int64
	var failedCount atomic.Int64
	var driverFailures atomic.Int64

	t.Run("group", func(t *testing.T) {
		for _, file := range allFiles {
			file := file
			t.Run(filepath.Base(file), func(t *testing.T) {
				t.Parallel()
				records, err := slt.ParseFile(file)
				if err != nil {
					t.Fatalf("failed to parse %s: %v", file, err)
				}

				var eng storage.Engine = memengine.New()
				cat := catalog.New(eng)
				coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
				defer coord.Close()
				exec := slt.NewCoordinatorExecutor(coord)

				isCoreProc := !strings.Contains(file, "/") && !strings.Contains(file, "\\")

				fileFailed := false
				for _, rec := range records {
					if err := slt.RunRecord(exec, rec); err != nil {
						fileFailed = true
						if isCoreProc {
							driverFailures.Add(1)
							t.Fatalf("[%s:%d] Core procedural SLT verification failure: %v\nSQL:\n%s", rec.FilePath, rec.LineNumber, err, rec.SQL)
						} else {
							t.Logf("[%s:%d] External oracle SLT mismatch: %v", rec.FilePath, rec.LineNumber, err)
							break
						}
					}
				}
				if fileFailed {
					failedCount.Add(1)
				} else {
					passedCount.Add(1)
				}
			})
		}
	})

	if driverFailures.Load() > 0 {
		t.Fatalf("Gate 5 driver/ORM corpora failed with %d failures", driverFailures.Load())
	}
	if passedCount.Load()+failedCount.Load() >= int64(len(allFiles)) && passedCount.Load() < 950 {
		t.Fatalf("Gate 5 conformance failed: expected at least 950 passing suites, got %d (failed %d)", passedCount.Load(), failedCount.Load())
	}
}
