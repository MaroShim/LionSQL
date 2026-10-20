package virtual_test

import (
	"testing"

	"lionsql/pkg/catalog/virtual"
	"lionsql/pkg/storage"
)

func TestDbGateMSSQL_CompositeForeignKeyAndActions(t *testing.T) {
	src := &mockSource{
		tables: []*storage.TableSchema{
			{
				Name: "parent_tbl",
				Columns: []storage.Column{
					{Name: "p1", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "p2", Type: storage.TypeInt, PrimaryKey: true},
				},
			},
			{
				Name: "child_tbl",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "c1", Type: storage.TypeInt},
					{Name: "c2", Type: storage.TypeInt},
				},
				ForeignKeys: []storage.ForeignKeySchema{
					{
						Name:          "fk_composite_child",
						ChildTable:    "child_tbl",
						ChildColumns:  []string{"c1", "c2"},
						ParentTable:   "parent_tbl",
						ParentColumns: []string{"p1", "p2"},
						OnUpdate:      storage.ActionCascade,
						OnDelete:      storage.ActionSetNull,
					},
				},
			},
		},
	}

	qFK := "SELECT * FROM INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS"
	res, err := virtual.Execute(src, qFK)
	if err != nil {
		t.Fatalf("virtual.Execute failed for foreign keys: %v", err)
	}

	// For a composite foreign key with 2 columns, it must produce 2 individual rows (one per column pair)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 FK rows for composite foreign key, got %d", len(res.Rows))
	}

	row0 := res.Rows[0]
	if row0["constraintName"] != "fk_composite_child" {
		t.Errorf("expected constraintName fk_composite_child, got %v", row0["constraintName"])
	}
	if row0["columnName"] != "c1" || row0["refColumnName"] != "p1" {
		t.Errorf("expected c1 -> p1 in row 0, got %v -> %v", row0["columnName"], row0["refColumnName"])
	}
	if row0["updateAction"] != "CASCADE" || row0["deleteAction"] != "SET NULL" {
		t.Errorf("expected CASCADE and SET NULL in row 0, got %v, %v", row0["updateAction"], row0["deleteAction"])
	}

	row1 := res.Rows[1]
	if row1["constraintName"] != "fk_composite_child" {
		t.Errorf("expected constraintName fk_composite_child in row 1, got %v", row1["constraintName"])
	}
	if row1["columnName"] != "c2" || row1["refColumnName"] != "p2" {
		t.Errorf("expected c2 -> p2 in row 1, got %v -> %v", row1["columnName"], row1["refColumnName"])
	}
	if row1["updateAction"] != "CASCADE" || row1["deleteAction"] != "SET NULL" {
		t.Errorf("expected CASCADE and SET NULL in row 1, got %v, %v", row1["updateAction"], row1["deleteAction"])
	}
}
