package slt_test

import (
	"path/filepath"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/engine"
	"lionsql/pkg/slt"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestSQLLogicTest_AllSuites(t *testing.T) {
	coreFiles, err := filepath.Glob("core/*.slt")
	if err != nil {
		t.Fatalf("failed to glob core files: %v", err)
	}
	wsFiles, err := filepath.Glob("whitespace/*.slt")
	if err != nil {
		t.Fatalf("failed to glob whitespace files: %v", err)
	}
	boundFiles, err := filepath.Glob("boundary/*.slt")
	if err != nil {
		t.Fatalf("failed to glob boundary files: %v", err)
	}

	allFiles := append(coreFiles, wsFiles...)
	allFiles = append(allFiles, boundFiles...)

	if len(allFiles) == 0 {
		t.Fatalf("no .slt test files discovered")
	}

	for _, file := range allFiles {
		t.Run(file, func(t *testing.T) {
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
