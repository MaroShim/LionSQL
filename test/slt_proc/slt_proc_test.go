package slt_proc_test

import (
	"path/filepath"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/slt"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func discoverProcSLTFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.slt")
	if err != nil {
		t.Fatalf("failed to glob .slt files: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no .slt test files discovered in test/slt_proc")
	}
	return files
}

func TestTSQL_StoredProcedure_Suites(t *testing.T) {
	allFiles := discoverProcSLTFiles(t)

	for _, file := range allFiles {
		t.Run(filepath.Base(file), func(t *testing.T) {
			records, err := slt.ParseFile(file)
			if err != nil {
				t.Fatalf("failed to parse %s: %v", file, err)
			}

			var eng storage.Engine = memengine.New()
			cat := catalog.New(eng)
			coord := engine.NewCoordinator(cat, eng, engine.DefaultConfig())
			exec := slt.NewCoordinatorExecutor(coord)

			for _, rec := range records {
				

if err := slt.RunRecord(exec, rec); err != nil {

					t.Fatalf("[%s:%d] SLT verification failure: %v\nSQL:\n%s", rec.FilePath, rec.LineNumber, err, rec.SQL)
				}
			}
		})
	}
}
