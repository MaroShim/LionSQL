package catalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/planner"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestCatalog_TableStats(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)

	stats := planner.NewTableStats("customers", 5000)
	cat.SetTableStats("customers", stats)

	retrieved, found := cat.GetTableStats("customers")
	if !found || retrieved == nil {
		t.Fatalf("expected to find table stats for customers")
	}
	if retrieved.RowCount != 5000 {
		t.Fatalf("expected row count 5000, got %d", retrieved.RowCount)
	}

	all := cat.ListTableStats()
	if len(all) != 1 || all["customers"] == nil {
		t.Fatalf("expected 1 table stats in list, got %d", len(all))
	}
}

func TestCatalog_DynamicTableRowCountFallback(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)

	schema := &storage.TableSchema{
		Name: "items",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "name", Type: storage.TypeVarChar},
		},
	}
	if err := eng.CreateTable(schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	sess, err := eng.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer sess.Close()

	tx, err := sess.BeginTx(storage.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(1), "name": storage.StringValue("A")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(2), "name": storage.StringValue("B")})
	_ = tx.Insert("items", storage.Record{"id": storage.IntValue(3), "name": storage.StringValue("C")})
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Dynamic fallback without explicit ANALYZE / SetTableStats
	stats, found := cat.GetTableStats("items")
	if !found || stats == nil {
		t.Fatalf("expected dynamic TableStats fallback to find 'items'")
	}
	if stats.RowCount != 3 {
		t.Fatalf("expected dynamic row count 3, got: %d", stats.RowCount)
	}
}

func TestCatalog_ProcedureDuplicateCheck(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)

	proc1 := &catalog.Procedure{
		Name: "TransferFunds",
		Body: "CREATE PROCEDURE TransferFunds AS BEGIN PRINT '1'; END;",
	}

	// 1. First registration via CreateProcedure succeeds
	if err := cat.CreateProcedure(proc1); err != nil {
		t.Fatalf("first CreateProcedure failed: %v", err)
	}

	// 2. Duplicate registration with exact same name fails with ErrProcExists
	procDup := &catalog.Procedure{
		Name: "TransferFunds",
		Body: "CREATE PROCEDURE TransferFunds AS BEGIN PRINT '2'; END;",
	}
	if err := cat.CreateProcedure(procDup); err == nil {
		t.Fatalf("expected ErrProcExists for duplicate procedure, got nil")
	}

	// 3. Duplicate registration with different casing (transferfunds) also fails with ErrProcExists
	procCase := &catalog.Procedure{
		Name: "transferfunds",
		Body: "CREATE PROCEDURE transferfunds AS BEGIN PRINT '3'; END;",
	}
	if err := cat.CreateProcedure(procCase); err == nil {
		t.Fatalf("expected ErrProcExists for case-different duplicate, got nil")
	}

	// 4. AlterProcedure updates existing procedure
	procAlter := &catalog.Procedure{
		Name: "TransferFunds",
		Body: "ALTER PROCEDURE TransferFunds AS BEGIN PRINT 'altered'; END;",
	}
	if err := cat.AlterProcedure(procAlter); err != nil {
		t.Fatalf("AlterProcedure failed: %v", err)
	}

	// 5. AlterProcedure on non-existent procedure fails with ErrProcNotFound
	procNonExistent := &catalog.Procedure{
		Name: "NonExistentProc",
		Body: "ALTER PROCEDURE NonExistentProc AS BEGIN PRINT 'nope'; END;",
	}
	if err := cat.AlterProcedure(procNonExistent); err == nil {
		t.Fatalf("expected ErrProcNotFound for AlterProcedure on non-existent proc, got nil")
	}

	// 6. Case-insensitive lookup
	p, err := cat.GetProcedure("transferfunds")
	if err != nil || p == nil {
		t.Fatalf("GetProcedure with lowercase failed: %v", err)
	}
	if p.Body != procAlter.Body {
		t.Errorf("expected altered body, got %q", p.Body)
	}

	// 7. ListProcedures should contain only 1 procedure
	procs := cat.ListProcedures()
	if len(procs) != 1 {
		t.Fatalf("expected exactly 1 procedure in list, got %d", len(procs))
	}
}

func TestCatalog_TableStatsPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sqlion_cbo_stats_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng1 := memengine.New()
	defer eng1.Close()
	cat1 := catalog.New(eng1)

	// 1. Enable persistence
	if err := cat1.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("SetPersistenceDir failed: %v", err)
	}
	if cat1.PersistenceDir() != tmpDir {
		t.Fatalf("expected persistence dir %s, got %s", tmpDir, cat1.PersistenceDir())
	}

	// 2. Build stats and save via SetTableStats
	ts := planner.NewTableStats("orders", 1000)
	vals := []storage.Value{
		storage.IntValue(10),
		storage.IntValue(50),
		storage.IntValue(100),
	}
	ts.Columns["total"] = planner.BuildColumnStats("total", storage.TypeInt, vals, 0, 1000)
	cat1.SetTableStats("orders", ts)

	// Verify cbo_stats.json exists physically on disk
	statsFile := filepath.Join(tmpDir, "cbo_stats.json")
	if _, err := os.Stat(statsFile); err != nil {
		t.Fatalf("expected %s to exist on disk: %v", statsFile, err)
	}

	// 3. Create fresh catalog in separate instance and load existing stats
	eng2 := memengine.New()
	defer eng2.Close()
	cat2 := catalog.New(eng2)

	if err := cat2.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("cat2 SetPersistenceDir failed: %v", err)
	}

	loadedStats, found := cat2.GetTableStats("orders")
	if !found || loadedStats == nil {
		t.Fatalf("expected cat2 to restore table stats for 'orders'")
	}
	if loadedStats.RowCount != 1000 {
		t.Fatalf("expected row count 1000, got %d", loadedStats.RowCount)
	}
	if col, ok := loadedStats.Columns["total"]; !ok || col.DistinctCount != 3 {
		t.Fatalf("expected col 'total' with distinct count 3, got %+v", col)
	}

	// 4. Test InvalidateTableStats auto-sync to disk
	cat2.InvalidateTableStats("orders")
	if _, found := cat2.GetTableStats("orders"); found {
		t.Fatalf("expected 'orders' stats to be invalidated")
	}

	// Check third catalog sees empty stats
	cat3 := catalog.New(eng2)
	if err := cat3.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("cat3 SetPersistenceDir failed: %v", err)
	}
	if _, found := cat3.GetTableStats("orders"); found {
		t.Fatalf("expected cat3 to find no stats after invalidation")
	}

	// 5. Explicit Save/Load file testing
	manualPath := filepath.Join(tmpDir, "manual_stats.json")
	cat1.SetTableStats("orders", ts)
	if err := cat1.SaveTableStatsToFile(manualPath); err != nil {
		t.Fatalf("SaveTableStatsToFile failed: %v", err)
	}
	cat4 := catalog.New(eng2)
	if err := cat4.LoadTableStatsFromFile(manualPath); err != nil {
		t.Fatalf("LoadTableStatsFromFile failed: %v", err)
	}
	if st, ok := cat4.GetTableStats("orders"); !ok || st.RowCount != 1000 {
		t.Fatalf("expected restored stats from manual file")
	}
}

func TestCatalog_ExtendedStatistics(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	stat := &catalog.StatisticDef{
		Name:      "stat_geo",
		TableName: "customers",
		Columns:   []string{"country", "city"},
	}

	// 1. Create statistics
	if err := cat.CreateStatistics(stat); err != nil {
		t.Fatalf("CreateStatistics failed: %v", err)
	}

	// 2. Get statistics
	got, ok := cat.GetStatistics("customers", "stat_geo")
	if !ok || got == nil {
		t.Fatalf("expected to find stat_geo in catalog")
	}
	if len(got.Columns) != 2 || got.Columns[0] != "country" || got.Columns[1] != "city" {
		t.Fatalf("unexpected columns in stat_geo: %+v", got.Columns)
	}

	// 3. List statistics
	list := cat.ListStatistics("customers")
	if len(list) != 1 || list[0].Name != "stat_geo" {
		t.Fatalf("expected 1 statistic in list, got %+v", list)
	}

	// 4. Drop statistics
	if err := cat.DropStatistics("customers", "stat_geo"); err != nil {
		t.Fatalf("DropStatistics failed: %v", err)
	}
	if _, ok := cat.GetStatistics("customers", "stat_geo"); ok {
		t.Fatalf("expected stat_geo to be dropped")
	}
}
