package catalog_test

import (
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/planner"
	"sqlion/pkg/storage/memengine"
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
