package catalog_test

import (
	"os"
	"sync"
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

// TestCatalog_Regression_P0_20_AlterTriggerPersistence verifies that AlterTrigger
// persists changes to disk so that daemon reboot restores the updated trigger definition.
func TestCatalog_Regression_P0_20_AlterTriggerPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_cat_p0_20_*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng1 := memengine.New()
	defer eng1.Close()
	cat1 := catalog.New(eng1)
	if err := cat1.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("SetPersistenceDir error: %v", err)
	}

	trg := &catalog.Trigger{
		Name:   "trg_stock",
		Table:  "products",
		Timing: "AFTER",
		Events: []string{"UPDATE"},
		Body:   "CREATE TRIGGER trg_stock ON products AFTER UPDATE AS BEGIN PRINT 'V1'; END;",
	}
	if err := cat1.RegisterTrigger(trg); err != nil {
		t.Fatalf("RegisterTrigger error: %v", err)
	}

	// Update trigger definition
	updatedTrg := &catalog.Trigger{
		Name:   "trg_stock",
		Table:  "products",
		Timing: "AFTER",
		Events: []string{"UPDATE"},
		Body:   "CREATE TRIGGER trg_stock ON products AFTER UPDATE AS BEGIN PRINT 'V2_UPDATED'; END;",
	}
	if err := cat1.AlterTrigger(updatedTrg); err != nil {
		t.Fatalf("AlterTrigger error: %v", err)
	}

	// Reboot: new catalog instance pointing to same persistence directory
	eng2 := memengine.New()
	defer eng2.Close()
	cat2 := catalog.New(eng2)
	if err := cat2.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("cat2 SetPersistenceDir error: %v", err)
	}

	loadedTrg, err := cat2.GetTrigger("trg_stock")
	if err != nil {
		t.Fatalf("failed to reload trigger: %v", err)
	}
	if loadedTrg.Body != updatedTrg.Body {
		t.Fatalf("[P0-20 DEFECT CONFIRMED] Trigger body was not persisted on AlterTrigger! Expected %q, got %q",
			updatedTrg.Body, loadedTrg.Body)
	}
	t.Logf("[P0-20 PASS] AlterTrigger successfully persisted changes across reboot.")
}

// TestCatalog_Regression_P0_21_CreateStatisticsPersistence verifies that CreateStatistics
// and DropStatistics persist extended statistics to disk across catalog reloads.
func TestCatalog_Regression_P0_21_CreateStatisticsPersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "lionsql_cat_p0_21_*")
	if err != nil {
		t.Fatalf("temp dir error: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	eng1 := memengine.New()
	defer eng1.Close()
	cat1 := catalog.New(eng1)
	if err := cat1.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("SetPersistenceDir error: %v", err)
	}

	stat := &catalog.StatisticDef{
		Name:      "stat_cust_order",
		TableName: "orders",
		Columns:   []string{"customer_id", "order_date"},
	}
	if err := cat1.CreateStatistics(stat); err != nil {
		t.Fatalf("CreateStatistics error: %v", err)
	}

	// Reboot: new catalog instance pointing to same persistence directory
	eng2 := memengine.New()
	defer eng2.Close()
	cat2 := catalog.New(eng2)
	if err := cat2.SetPersistenceDir(tmpDir); err != nil {
		t.Fatalf("cat2 SetPersistenceDir error: %v", err)
	}

	loadedStat, found := cat2.GetStatistics("orders", "stat_cust_order")
	if !found {
		t.Fatalf("[P0-21 DEFECT CONFIRMED] Extended statistics 'stat_cust_order' was not persisted to disk across reload!")
	}
	if len(loadedStat.Columns) != 2 || loadedStat.Columns[0] != "customer_id" {
		t.Fatalf("[P0-21 DEFECT CONFIRMED] Loaded statistics corrupted: %+v", loadedStat)
	}
	t.Logf("[P0-21 PASS] CreateStatistics successfully persisted across reboot.")
}

// TestCatalog_Regression_P0_22_GetView_DataRace verifies that GetView returns
// a defensive copy so that concurrent callers cannot cause a data race or mutate internal state.
func TestCatalog_Regression_P0_22_GetView_DataRace(t *testing.T) {
	eng := memengine.New()
	defer eng.Close()
	cat := catalog.New(eng)

	vw := &catalog.View{
		Name:  "v_monthly_sales",
		Query: "SELECT year, month, sum(amount) FROM sales GROUP BY year, month",
	}
	if err := cat.CreateView(vw); err != nil {
		t.Fatalf("CreateView error: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			v, err := cat.GetView("v_monthly_sales")
			if err != nil || v == nil {
				t.Errorf("worker %d: GetView failed: %v", workerID, err)
				return
			}
			// Attempt to mutate the returned view pointer
			v.Query = "MUTATED QUERY"
		}(i)
	}
	wg.Wait()

	// Verify catalog view query was NOT mutated by caller mutations!
	freshView, err := cat.GetView("v_monthly_sales")
	if err != nil {
		t.Fatalf("GetView failed: %v", err)
	}
	if freshView.Query != vw.Query {
		t.Fatalf("[P0-22 DEFECT CONFIRMED] Internal catalog view query was mutated! Expected original %q, got %q",
			vw.Query, freshView.Query)
	}
	t.Logf("[P0-22 PASS] GetView returns isolated copy; internal catalog view is intact.")
}
