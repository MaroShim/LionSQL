package slt_test

import (
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/slt"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func runSLTFile(t *testing.T, file string) {
	if testing.Short() {
		t.Skip("skipping full SLT file in short mode")
	}
	records, err := slt.ParseFile(file)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", file, err)
	}

	var eng storage.Engine = memengine.New()
	cat := catalog.New(eng)
	coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
	exec := slt.NewCoordinatorExecutor(coord)

	passCount := 0
	for i, rec := range records {
		if i%500 == 0 {
			t.Logf("[%s] Progress: %d / %d records passed", file, i, len(records))
		}
		start := time.Now()
		if err := slt.RunRecord(exec, rec); err != nil {
			t.Fatalf("[%s:%d] record #%d SLT verification failure: %v\nSQL:\n%s", rec.FilePath, rec.LineNumber, i, err, rec.SQL)
		}
		dur := time.Since(start)
		if dur > 200*time.Millisecond {
			t.Logf("[%s] Slow record #%d (took %v): %s", file, i, dur, rec.SQL)
		}
		passCount++
	}
	t.Logf("Passed %d records in %s", passCount, file)
}

func TestSQLLogicTest_Select1_InProc(t *testing.T) {
	runSLTFile(t, "full/select1.test")
}

func TestSQLLogicTest_Select2_InProc(t *testing.T) {
	runSLTFile(t, "full/select2.test")
}

func TestSQLLogicTest_Select3_InProc(t *testing.T) {
	runSLTFile(t, "full/select3.test")
}

func TestSQLLogicTest_Select4_InProc(t *testing.T) {
	runSLTFile(t, "full/select4.test")
}

func TestSQLLogicTest_Select5_InProc(t *testing.T) {
	runSLTFile(t, "full/select5.test")
}
