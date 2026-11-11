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

func TestMSSQL_ReferentialConstraints_ReferencedObjectID(t *testing.T) {
	src := &mockSource{
		tables: []*storage.TableSchema{
			{
				Name: "t1_users",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
				},
			},
			{
				Name: "t2_orders",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
				},
			},
			{
				Name: "t3_items",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "order_id", Type: storage.TypeInt},
				},
				ForeignKeys: []storage.ForeignKeySchema{
					{
						Name:          "fk_items_order",
						ChildTable:    "t3_items",
						ChildColumns:  []string{"order_id"},
						ParentTable:   "t2_orders",
						ParentColumns: []string{"id"},
					},
				},
			},
		},
	}

	q := "SELECT * FROM INFORMATION_SCHEMA.REFERENTIAL_CONSTRAINTS"
	res, err := virtual.Execute(src, q)
	if err != nil {
		t.Fatalf("virtual.Execute failed: %v", err)
	}

	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 FK row, got %d", len(res.Rows))
	}

	row := res.Rows[0]
	// t1_users is 1000, t2_orders is 1001, t3_items is 1002
	if row["parent_object_id"] != 1002 {
		t.Errorf("expected parent_object_id 1002 (t3_items), got %v", row["parent_object_id"])
	}
	if row["referenced_object_id"] != 1001 {
		t.Errorf("expected referenced_object_id 1001 (t2_orders), got %v", row["referenced_object_id"])
	}

	// 2. Bracket and schema-qualified parent table name ([dbo].[t2_orders]), unnamed FK, and non-existent parent table
	srcEdge := &mockSource{
		tables: []*storage.TableSchema{
			{
				Name: "t1_users",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
				},
			},
			{
				Name: "t2_orders",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
				},
			},
			{
				Name: "t3_items",
				Columns: []storage.Column{
					{Name: "id", Type: storage.TypeInt, PrimaryKey: true},
					{Name: "order_id", Type: storage.TypeInt},
					{Name: "cust_id", Type: storage.TypeInt},
					{Name: "ghost_id", Type: storage.TypeInt},
				},
				ForeignKeys: []storage.ForeignKeySchema{
					{
						// Unnamed FK with bracket/schema parent table
						Name:          "",
						ChildTable:    "t3_items",
						ChildColumns:  []string{"order_id"},
						ParentTable:   "[dbo].[t2_orders]",
						ParentColumns: []string{"id"},
					},
					{
						// Explicit schema parent table
						Name:          "fk_items_cust",
						ChildTable:    "t3_items",
						ChildColumns:  []string{"cust_id"},
						ParentTable:   "sales.customers",
						ParentColumns: []string{"id"},
					},
					{
						// Non-existent parent table: must resolve to 0, NOT 1000 (t1_users)
						Name:          "fk_items_ghost",
						ChildTable:    "t3_items",
						ChildColumns:  []string{"ghost_id"},
						ParentTable:   "ghost_table",
						ParentColumns: []string{"id"},
					},
				},
			},
		},
	}

	resEdge, err := virtual.Execute(srcEdge, q)
	if err != nil {
		t.Fatalf("virtual.Execute failed: %v", err)
	}
	if len(resEdge.Rows) != 3 {
		t.Fatalf("expected 3 FK rows, got %d", len(resEdge.Rows))
	}

	// Row 0: Bracket-qualified [dbo].[t2_orders] and unnamed FK
	r0 := resEdge.Rows[0]
	if r0["referenced_object_id"] != 1001 {
		t.Errorf("expected referenced_object_id 1001 for [dbo].[t2_orders], got %v", r0["referenced_object_id"])
	}
	if r0["constraintName"] != "FK_t3_items_t2_orders" {
		t.Errorf("expected generated constraintName 'FK_t3_items_t2_orders', got %v", r0["constraintName"])
	}

	// Row 1: sales.customers
	r1 := resEdge.Rows[1]
	if r1["refSchemaName"] != "sales" {
		t.Errorf("expected refSchemaName 'sales', got %v", r1["refSchemaName"])
	}

	// Row 2: ghost_table
	r2 := resEdge.Rows[2]
	if r2["referenced_object_id"] != 0 {
		t.Errorf("expected referenced_object_id 0 for non-existent parent table, got %v", r2["referenced_object_id"])
	}
}


