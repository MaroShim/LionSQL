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
	subFiles, _ := filepath.Glob("*/*.slt")
	files = append(files, subFiles...)
	if len(files) == 0 {
		t.Fatalf("no .slt test files discovered in test/slt_proc")
	}
	return files
}

func TestTSQL_StoredProcedure_Suites(t *testing.T) {
	allFiles := discoverProcSLTFiles(t)
	if len(allFiles) < 1289 {
		t.Fatalf("expected at least 1,289 authentic Gate 5 T-SQL & External Corpora .slt suites, found %d", len(allFiles))
	}

	requiredDirs := []string{
		"babelfish_oracle",
		"sqlglot_oracle",
		"tedious_oracle",
		"pymssql_oracle",
		"gorm_oracle",
		"mssql_jdbc_oracle",
	}
	for _, dir := range requiredDirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.slt"))
		if len(matches) == 0 {
			t.Fatalf("expected Gate 5 external corpus directory %q to contain .slt suites, found 0", dir)
		}
	}

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
