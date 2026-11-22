package slt_test

import (
	"os"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/slt"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
	"lionsql/pkg/wire"
	"lionsql/pkg/wire/tds"
)

// TestSQLLogicTest_Duality_DualWire executes SQLLogicTest core suites simultaneously over
// TDS (:1433) and PGWire (:5433), asserting 100% 1:1 cross-protocol parity on every single query (SLT-DUALITY-WIRE-1).
func TestSQLLogicTest_Duality_DualWire(t *testing.T) {
	testSuites := discoverSLTFiles(t)

	for _, file := range testSuites {
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

			// Track B: PGWire Server backed by isolated Engine B
			var engPG storage.Engine = memengine.New()
			_ = engPG.Open("")
			defer engPG.Close()
			catPG := catalog.New(engPG)
			coordPG := engine.NewCoordinator(catPG, engPG, engine.DefaultConfig())

			pgSrv := wire.NewServer("127.0.0.1:0", catPG, engPG)
			pgSrv.SetCoordinator(coordPG)
			if err := pgSrv.Start(); err != nil {
				t.Fatalf("failed to start PGWire server: %v", err)
			}
			defer pgSrv.Close()

			tdsExec, err := slt.NewTDSExecutor(tdsSrv.Addr().String())
			if err != nil {
				t.Fatalf("failed to connect TDS executor: %v", err)
			}
			defer tdsExec.Close()

			pgExec, err := slt.NewPGWireExecutor(pgSrv.Addr().String())
			if err != nil {
				t.Fatalf("failed to connect PGWire executor: %v", err)
			}
			defer pgExec.Close()

			// Wrap both in DualityExecutor: every query runs over TDS AND PGWire
			dualityExec := slt.NewDualityExecutor(tdsExec, "TDS_1433", pgExec, "PGWire_5433")

			for _, rec := range records {
				if err := slt.RunRecord(dualityExec, rec); err != nil {
					t.Fatalf("[%s:%d] Duality Dual-Wire mismatch failure: %v\nSQL:\n%s",
						rec.FilePath, rec.LineNumber, err, rec.SQL)
				}
			}
		})
	}
}

// TestSQLLogicTest_Duality_DualStorage executes SQLLogicTest suites simultaneously on
// In-Memory OCC and WiredTiger B+Tree, asserting 1:1 storage ACID and tuple parity (SLT-DUALITY-STORAGE-1).
func TestSQLLogicTest_Duality_DualStorage(t *testing.T) {
	testSuites := discoverSLTFiles(t)

	for _, file := range testSuites {
		t.Run(file, func(t *testing.T) {
			records, err := slt.ParseFile(file)
			if err != nil {
				t.Fatalf("failed to parse %s: %v", file, err)
			}

			// Track A: In-Memory OCC
			engA := memengine.New()
			_ = engA.Open("")
			defer engA.Close()
			catA := catalog.New(engA)
			coordA := engine.NewCoordinator(catA, engA, engine.DefaultConfig())
			execA := slt.NewCoordinatorExecutor(coordA)

			// Track B: WiredTiger MVCC B+Tree
			tmpDir, err := os.MkdirTemp("", "slt_wt_*")
			if err != nil {
				t.Fatalf("failed to create wt temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			engB := wiredtiger.New()
			if err := engB.Open(tmpDir); err != nil {
				t.Skipf("skipping WiredTiger duality test: %v", err)
			}
			defer engB.Close()
			catB := catalog.New(engB)
			coordB := engine.NewCoordinator(catB, engB, engine.DefaultConfig())
			execB := slt.NewCoordinatorExecutor(coordB)

			// Wrap in DualityExecutor: every query runs on MemEngine AND WiredTiger
			dualityExec := slt.NewDualityExecutor(execA, "MemEngine_OCC", execB, "WiredTiger_MVCC")

			for _, rec := range records {
				if err := slt.RunRecord(dualityExec, rec); err != nil {
					t.Fatalf("[%s:%d] Duality Dual-Storage mismatch failure: %v\nSQL:\n%s",
						rec.FilePath, rec.LineNumber, err, rec.SQL)
				}
			}
		})
	}
}
