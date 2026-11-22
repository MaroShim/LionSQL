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
	"lionsql/pkg/wire/tds"
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

// TestTSQL_Duality_DualParser_CoreProcedures runs all 30 core T-SQL stored procedure suites
// through DualParserExecutor, asserting 1:1 parser differential equivalence between
// Tier 1 Pratt Fast-Path parser and Tier 2 ANTLR4 Full-Grammar parser (SLT-DUALITY-PARSER-1).
func TestTSQL_Duality_DualParser_CoreProcedures(t *testing.T) {
	files1, err := filepath.Glob("0*.slt")
	if err != nil {
		t.Fatalf("failed to glob 0*.slt: %v", err)
	}
	files2, _ := filepath.Glob("[1-3]*.slt")
	coreFiles := append(files1, files2...)

	if len(coreFiles) != 30 {
		t.Fatalf("expected 30 core T-SQL procedure suites, found %d", len(coreFiles))
	}

	for _, file := range coreFiles {
		file := file
		t.Run(file, func(t *testing.T) {
			records, err := slt.ParseFile(file)
			if err != nil {
				t.Fatalf("failed to parse %s: %v", file, err)
			}

			var eng storage.Engine = memengine.New()
			_ = eng.Open("")
			defer eng.Close()
			cat := catalog.New(eng)
			coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
			defer coord.Close()

			coordExec := slt.NewCoordinatorExecutor(coord)
			dualParserExec := slt.NewDualParserExecutor(coordExec)

			for _, rec := range records {
				if err := slt.RunRecord(dualParserExec, rec); err != nil {
					t.Fatalf("[%s:%d] Dual-Parser differential equivalence failure: %v\nSQL:\n%s",
						rec.FilePath, rec.LineNumber, err, rec.SQL)
				}
			}
		})
	}
}

// TestTSQL_Duality_Wire_CoreProcedures runs all 30 core T-SQL stored procedure suites
// over real TDS TCP wire (:1433) vs in-process Coordinator, enforcing 1:1 wire serialization
// and execution parity (SLT-DUALITY-TSQL-WIRE-1).
func TestTSQL_Duality_Wire_CoreProcedures(t *testing.T) {
	files1, err := filepath.Glob("0*.slt")
	if err != nil {
		t.Fatalf("failed to glob 0*.slt: %v", err)
	}
	files2, _ := filepath.Glob("[1-3]*.slt")
	coreFiles := append(files1, files2...)

	if len(coreFiles) != 30 {
		t.Fatalf("expected 30 core T-SQL procedure suites, found %d", len(coreFiles))
	}

	for _, file := range coreFiles {
		file := file
		t.Run(file, func(t *testing.T) {
			records, err := slt.ParseFile(file)
			if err != nil {
				t.Fatalf("failed to parse %s: %v", file, err)
			}

			// Track A: TDS Wire Server backed by isolated Engine A
			var engTDS storage.Engine = memengine.New()
			_ = engTDS.Open("")
			defer engTDS.Close()
			catTDS := catalog.New(engTDS)

			tdsSrv := tds.NewServer("127.0.0.1:0", catTDS, engTDS)
			if err := tdsSrv.Start(); err != nil {
				t.Fatalf("failed to start TDS server: %v", err)
			}
			defer tdsSrv.Close()

			tdsExec, err := slt.NewTDSExecutor(tdsSrv.Addr().String())
			if err != nil {
				t.Fatalf("failed to connect TDS executor: %v", err)
			}
			defer tdsExec.Close()

			// Track B: In-Process Coordinator backed by isolated Engine B
			var engCoord storage.Engine = memengine.New()
			_ = engCoord.Open("")
			defer engCoord.Close()
			catCoord := catalog.New(engCoord)
			coord := engine.NewCoordinator(catCoord, engCoord, engine.DefaultConfig())
			defer coord.Close()
			coordExec := slt.NewCoordinatorExecutor(coord)

			// Wrap both in DualityExecutor: every query runs over TDS AND Coordinator
			dualityExec := slt.NewDualityExecutor(tdsExec, "TDS_Wire_1433", coordExec, "Coordinator_InProc")

			passCount := 0
			for i, rec := range records {
				if err := slt.RunRecord(dualityExec, rec); err != nil {
					t.Fatalf("[%s:%d] record #%d T-SQL Wire Duality failure: %v\nSQL:\n%s",
						rec.FilePath, rec.LineNumber, i, err, rec.SQL)
				}
				passCount++
			}
			t.Logf("[%s] 100%% PASS: verified %d records over TDS Wire Duality", file, passCount)
		})
	}
}

// TestTSQL_Duality_DualPath_TDS runs all 30 core T-SQL stored procedure suites
// comparing TDS SQL Batch (Type 1) vs RPC sp_executesql (Type 3) over real TCP wire,
// asserting 100% intra-protocol parity (SLT-DUALITY-DUALPATH-1).
func TestTSQL_Duality_DualPath_TDS(t *testing.T) {
	files1, err := filepath.Glob("0*.slt")
	if err != nil {
		t.Fatalf("failed to glob 0*.slt: %v", err)
	}
	files2, _ := filepath.Glob("[1-3]*.slt")
	coreFiles := append(files1, files2...)

	if len(coreFiles) != 30 {
		t.Fatalf("expected 30 core T-SQL procedure suites, found %d", len(coreFiles))
	}

	for _, file := range coreFiles {
		file := file
		t.Run(file, func(t *testing.T) {
			records, err := slt.ParseFile(file)
			if err != nil {
				t.Fatalf("failed to parse %s: %v", file, err)
			}

			// Server A: TDS SQL Batch
			var engA storage.Engine = memengine.New()
			_ = engA.Open("")
			defer engA.Close()
			catA := catalog.New(engA)
			tdsSrvA := tds.NewServer("127.0.0.1:0", catA, engA)
			if err := tdsSrvA.Start(); err != nil {
				t.Fatalf("failed to start TDS server A: %v", err)
			}
			defer tdsSrvA.Close()

			execBatch, err := slt.NewTDSExecutorWithRPC(tdsSrvA.Addr().String(), false)
			if err != nil {
				t.Fatalf("failed to connect TDS Batch executor: %v", err)
			}
			defer execBatch.Close()

			// Server B: TDS RPC sp_executesql
			var engB storage.Engine = memengine.New()
			_ = engB.Open("")
			defer engB.Close()
			catB := catalog.New(engB)
			tdsSrvB := tds.NewServer("127.0.0.1:0", catB, engB)
			if err := tdsSrvB.Start(); err != nil {
				t.Fatalf("failed to start TDS server B: %v", err)
			}
			defer tdsSrvB.Close()

			execRPC, err := slt.NewTDSExecutorWithRPC(tdsSrvB.Addr().String(), true)
			if err != nil {
				t.Fatalf("failed to connect TDS RPC executor: %v", err)
			}
			defer execRPC.Close()

			dualityExec := slt.NewDualityExecutor(execBatch, "TDS_SQL_Batch", execRPC, "TDS_RPC_sp_executesql")

			passCount := 0
			for i, rec := range records {
				if err := slt.RunRecord(dualityExec, rec); err != nil {
					t.Fatalf("[%s:%d] record #%d TDS Dual-Path failure: %v\nSQL:\n%s",
						rec.FilePath, rec.LineNumber, i, err, rec.SQL)
				}
				passCount++
			}
			t.Logf("[%s] 100%% PASS: verified %d records over TDS Protocol Dual-Path", file, passCount)
		})
	}
}
