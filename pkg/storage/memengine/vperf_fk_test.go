package memengine_test

import (
	"testing"

	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
)

func TestVPerf_FKReverseCache(t *testing.T) {
	engine := memengine.New()

	// Initial check on non-existent or empty table
	refs := engine.GetChildFKRefs("parent_tab")
	if len(refs) != 0 {
		t.Fatalf("expected 0 child FK refs, got %d", len(refs))
	}

	parentSchema := &storage.TableSchema{
		Name: "parent_tab",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
		},
	}
	if err := engine.CreateTable(parentSchema); err != nil {
		t.Fatalf("CreateTable parent failed: %v", err)
	}

	childSchema := &storage.TableSchema{
		Name: "child_tab",
		Columns: []storage.Column{
			{Name: "cid", Type: storage.TypeInt, PrimaryKey: true},
			{Name: "pid", Type: storage.TypeInt},
		},
		ForeignKeys: []storage.ForeignKeySchema{
			{
				Name:          "fk_child_parent",
				ChildColumns:  []string{"pid"},
				ParentTable:   "parent_tab",
				ParentColumns: []string{"id"},
			},
		},
	}
	if err := engine.CreateTable(childSchema); err != nil {
		t.Fatalf("CreateTable child failed: %v", err)
	}

	// Verify reverse FK cache indexed child_tab under parent_tab
	refsAfter := engine.GetChildFKRefs("parent_tab")
	if len(refsAfter) != 1 {
		t.Fatalf("expected 1 child FK ref for parent_tab, got %d", len(refsAfter))
	}
	if refsAfter[0].ChildTable != "child_tab" || refsAfter[0].FK.Name != "fk_child_parent" {
		t.Fatalf("unexpected child FK ref: %+v", refsAfter[0])
	}

	// Another table with no child references
	otherRefs := engine.GetChildFKRefs("child_tab")
	if len(otherRefs) != 0 {
		t.Fatalf("expected 0 child FK refs for child_tab, got %d", len(otherRefs))
	}
}
