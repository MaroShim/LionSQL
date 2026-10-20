package backup

import (
	"bytes"
	"errors"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestBackupAndRestore_Views(t *testing.T) {
	eng, cat := setupTestDB(t)
	defer eng.Close()

	// Register a View in the catalog
	vw := &catalog.View{
		Name:  "v_active_users",
		Query: "SELECT id, username FROM users WHERE is_active = true",
	}
	if err := cat.CreateView(vw); err != nil {
		t.Fatalf("CreateView failed: %v", err)
	}

	// 1. Create Backup
	var buf bytes.Buffer
	manifest, err := CreateBackup(&buf, eng, cat)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}
	if manifest.ViewsCount != 1 {
		t.Fatalf("expected ViewsCount == 1, got %d", manifest.ViewsCount)
	}

	// 2. Restore into fresh engine & catalog
	freshEng := memengine.New()
	if err := freshEng.Open(""); err != nil {
		t.Fatalf("freshEng Open failed: %v", err)
	}
	defer freshEng.Close()
	freshCat := catalog.New(freshEng)

	report, err := RestoreBackup(&buf, freshEng, freshCat, DefaultRestoreOptions())
	if err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}
	if report.ViewsRestored != 1 {
		t.Fatalf("expected ViewsRestored == 1, got %d", report.ViewsRestored)
	}

	restoredView, err := freshCat.GetView("v_active_users")
	if err != nil || restoredView == nil {
		t.Fatalf("failed to retrieve restored view: %v", err)
	}
	if restoredView.Query != vw.Query {
		t.Errorf("expected view query %q, got %q", vw.Query, restoredView.Query)
	}
}

type errEngineWrapper struct {
	storage.Engine
}

func (w *errEngineWrapper) CreateTable(schema *storage.TableSchema) error {
	return errors.New("simulated DDL disk I/O error on create table")
}

func TestPITR_DDLErrorPropagation(t *testing.T) {
	eng := memengine.New()
	if err := eng.Open(""); err != nil {
		t.Fatalf("failed to open memengine: %v", err)
	}
	defer eng.Close()

	wrapped := &errEngineWrapper{Engine: eng}

	schema := &storage.TableSchema{
		Name: "t1",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}

	rec := memengine.WALRecord{
		TxID: 100,
		Ops: []memengine.WALOp{
			{
				Type:   memengine.OpCreateTable,
				Table:  "t1",
				Schema: schema,
			},
		},
	}

	// Applying this WAL record should return an explicit error and not silently swallow it
	err := ApplyWALRecordWithOptions(wrapped, rec, nil)
	if err == nil {
		t.Fatalf("expected ApplyWALRecordWithOptions to fail on DDL error, got nil")
	}
}
