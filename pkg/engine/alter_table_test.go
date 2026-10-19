package engine

import (
	"testing"

	"sqlion/pkg/catalog"
	"sqlion/pkg/storage"
	"sqlion/pkg/storage/memengine"
)

func TestAlterTable_AddColumn(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	// 1. Create initial table
	createSQL := `CREATE TABLE employees (
		id INT PRIMARY KEY,
		name VARCHAR(100) NOT NULL,
		salary FLOAT NOT NULL
	);`
	res, err := coord.ExecuteSQL(createSQL)
	if err != nil || !res.Success {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. Insert sample rows
	_, err = coord.ExecuteSQL(`INSERT INTO employees (id, name, salary) VALUES (1, 'Alice', 50000.0);`)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// 3. ALTER TABLE ADD COLUMN email VARCHAR(255)
	alterSQL := `ALTER TABLE employees ADD COLUMN email VARCHAR(255);`
	res, err = coord.ExecuteSQL(alterSQL)
	if err != nil || !res.Success {
		t.Fatalf("ALTER TABLE ADD COLUMN failed: %v", err)
	}

	// 4. Verify table schema has 4 columns
	tbl, err := eng.GetTable("employees")
	if err != nil {
		t.Fatalf("GetTable failed: %v", err)
	}
	if len(tbl.Columns) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(tbl.Columns))
	}
	if tbl.Columns[3].Name != "email" || tbl.Columns[3].Type != storage.TypeVarChar {
		t.Fatalf("unexpected 4th column: %+v", tbl.Columns[3])
	}

	// 5. Query existing rows - new column should be NULL
	selRes, err := coord.ExecuteSQL(`SELECT id, name, salary, email FROM employees WHERE id = 1;`)
	if err != nil || !selRes.Success {
		t.Fatalf("SELECT failed: %v", err)
	}
	if len(selRes.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(selRes.Rows))
	}
	if selRes.Rows[0]["email"] != nil {
		t.Fatalf("expected nil/NULL for existing row's added column, got: %v", selRes.Rows[0]["email"])
	}

	// 6. Insert new row with the added column
	_, err = coord.ExecuteSQL(`INSERT INTO employees (id, name, salary, email) VALUES (2, 'Bob', 60000.0, 'bob@example.com');`)
	if err != nil {
		t.Fatalf("INSERT with new column failed: %v", err)
	}

	// 7. Verify new row has the value
	selRes2, err := coord.ExecuteSQL(`SELECT email FROM employees WHERE id = 2;`)
	if err != nil || !selRes2.Success || len(selRes2.Rows) == 0 {
		t.Fatalf("SELECT id=2 failed: %v", err)
	}
	if selRes2.Rows[0]["email"] != "bob@example.com" {
		t.Fatalf("expected 'bob@example.com', got: %v", selRes2.Rows[0]["email"])
	}

	// 8. ALTER TABLE ADD COLUMN without COLUMN keyword
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ADD department VARCHAR(50);`)
	if err != nil {
		t.Fatalf("ALTER TABLE ADD without COLUMN keyword failed: %v", err)
	}
	tbl2, _ := eng.GetTable("employees")
	if len(tbl2.Columns) != 5 {
		t.Fatalf("expected 5 columns, got %d", len(tbl2.Columns))
	}

	// 9. Adding duplicate column should return an error
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ADD email VARCHAR(100);`)
	if err == nil {
		t.Fatalf("expected error when adding duplicate column 'email', got nil")
	}
}
