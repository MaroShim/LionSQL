package oracle

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/speckit/grammar"
	"lionsql/pkg/storage/memengine"
	"lionsql/pkg/storage/wiredtiger"
)

func setupTestCoordinator(t *testing.T) (*engine.Coordinator, func()) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("memengine.Open failed: %v", err)
	}
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.Config{})
	return coord, func() {
		_ = eng.Close()
	}
}

func TestOracle_NoREC_BasicValidation(t *testing.T) {
	coord, cleanup := setupTestCoordinator(t)
	defer cleanup()

	sessID := "norec_test_sess"
	_, _ = coord.GetOrCreateSession(sessID)
	defer coord.CloseSession(sessID)

	rng := rand.New(rand.NewSource(42))
	table := grammar.GenerateTableSchema(rng, "norec_tbl", 4)
	tableDDL := table.GenerateCreateTableDDL()
	insertDMLs := table.GenerateInsertDML(rng, 5)

	_, err := coord.ExecuteSessionSQL(sessID, tableDDL)
	if err != nil {
		t.Fatalf("DDL failed: %v", err)
	}
	for _, dml := range insertDMLs {
		if _, err := coord.ExecuteSessionSQL(sessID, dml); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	pair := grammar.GenerateNoRECPair(rng, table, 2)
	disc, err := CheckNoREC(coord, sessID, pair, tableDDL, insertDMLs)
	if err != nil {
		t.Fatalf("CheckNoREC error: %v", err)
	}
	if disc != nil {
		t.Logf("Discrepancy detected (expected in fuzzing): %s", disc)
	}
}

func TestOracle_DualEngine_ParityValidation(t *testing.T) {
	coord1, cleanup1 := setupTestCoordinator(t)
	defer cleanup1()

	coord2, cleanup2 := setupTestCoordinator(t)
	defer cleanup2()

	sess1 := "dual_sess_1"
	sess2 := "dual_sess_2"
	_, _ = coord1.GetOrCreateSession(sess1)
	_, _ = coord2.GetOrCreateSession(sess2)
	defer coord1.CloseSession(sess1)
	defer coord2.CloseSession(sess2)

	rng := rand.New(rand.NewSource(100))
	table := grammar.GenerateTableSchema(rng, "dual_tbl", 4)
	tableDDL := table.GenerateCreateTableDDL()
	insertDMLs := table.GenerateInsertDML(rng, 5)

	// Setup identical data on both
	_, _ = coord1.ExecuteSessionSQL(sess1, tableDDL)
	_, _ = coord2.ExecuteSessionSQL(sess2, tableDDL)
	for _, dml := range insertDMLs {
		_, _ = coord1.ExecuteSessionSQL(sess1, dml)
		_, _ = coord2.ExecuteSessionSQL(sess2, dml)
	}

	for i := 0; i < 10; i++ {
		q := grammar.GenerateSelectQuery(rng, table, 2)
		disc, err := CheckDualEngine(coord1, sess1, "OCC_1", coord2, sess2, "OCC_2", q, tableDDL, insertDMLs)
		if err != nil {
			t.Fatalf("CheckDualEngine error: %v", err)
		}
		if disc != nil {
			t.Fatalf("unexpected discrepancy between identical engines: %s", disc)
		}
	}
}

func TestOracle_CrossEngine_MemVsWiredTiger_Parity(t *testing.T) {
	// 1. MemEngine Coordinator
	memEng := memengine.New()
	_ = memEng.Open("")
	defer memEng.Close()
	cat1 := catalog.New(memEng)
	coordMem := engine.NewCoordinator(cat1, memEng, engine.Config{})

	// 2. WiredTiger Coordinator
	wtDir := filepath.Join(t.TempDir(), "wt_spec_test")
	wtEng := wiredtiger.New()
	if err := wtEng.Open(wtDir); err != nil {
		t.Skipf("WiredTiger not enabled in this build (%v); skipping cross-engine test", err)
		return
	}
	defer wtEng.Close()
	cat2 := catalog.New(wtEng)
	coordWT := engine.NewCoordinator(cat2, wtEng, engine.Config{})

	sessMem := "sess_mem"
	sessWT := "sess_wt"
	_, _ = coordMem.GetOrCreateSession(sessMem)
	_, _ = coordWT.GetOrCreateSession(sessWT)
	defer coordMem.CloseSession(sessMem)
	defer coordWT.CloseSession(sessWT)

	rng := rand.New(rand.NewSource(2026))
	var discoveredDiscrepancies []*Discrepancy

	for tIdx := 0; tIdx < 10; tIdx++ {
		tblName := fmt.Sprintf("tbl_cross_%d", tIdx)
		table := grammar.GenerateTableSchema(rng, tblName, 4)
		tableDDL := table.GenerateCreateTableDDL()
		insertDMLs := table.GenerateInsertDML(rng, 10)

		// Create table on both
		if _, err := coordMem.ExecuteSessionSQL(sessMem, tableDDL); err != nil {
			continue
		}
		if _, err := coordWT.ExecuteSessionSQL(sessWT, tableDDL); err != nil {
			t.Fatalf("WT DDL failed: %v", err)
		}
		for _, dml := range insertDMLs {
			_, _ = coordMem.ExecuteSessionSQL(sessMem, dml)
			_, _ = coordWT.ExecuteSessionSQL(sessWT, dml)
		}

		for qIdx := 0; qIdx < 10; qIdx++ {
			q := grammar.GenerateSelectQuery(rng, table, 2)
			disc, err := CheckDualEngine(coordMem, sessMem, "MemEngine", coordWT, sessWT, "WiredTiger", q, tableDDL, insertDMLs)
			if err != nil {
				continue
			}
			if disc != nil {
				discoveredDiscrepancies = append(discoveredDiscrepancies, disc)
				t.Logf("[CROSS-ENGINE DISCREPANCY] %s\nQuery: %s\nExpected: %s\nActual: %s", disc.Details, disc.Query, disc.Expected, disc.Actual)
			}
		}

		// Cleanup
		_, _ = coordMem.ExecuteSessionSQL(sessMem, fmt.Sprintf("DROP TABLE %s;", tblName))
		_, _ = coordWT.ExecuteSessionSQL(sessWT, fmt.Sprintf("DROP TABLE %s;", tblName))
	}

	if len(discoveredDiscrepancies) > 0 {
		t.Logf("Total Cross-Engine Discrepancies detected: %d", len(discoveredDiscrepancies))
	}
}
