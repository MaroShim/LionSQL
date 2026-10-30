package engine

import (
	"testing"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage"
	"lionsql/pkg/storage/memengine"
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

	// 10. ALTER TABLE ADD CONSTRAINT UNIQUE
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ADD CONSTRAINT uq_emp_email UNIQUE (email);`)
	if err != nil {
		t.Fatalf("ALTER TABLE ADD CONSTRAINT UNIQUE failed: %v", err)
	}

	// 11. ALTER TABLE ADD CONSTRAINT CHECK
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ADD CONSTRAINT chk_salary CHECK (salary > 0);`)
	if err != nil {
		t.Fatalf("ALTER TABLE ADD CONSTRAINT CHECK failed: %v", err)
	}

	// 12. ALTER TABLE ALTER COLUMN DROP NOT NULL
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ALTER COLUMN name DROP NOT NULL;`)
	if err != nil {
		t.Fatalf("ALTER TABLE ALTER COLUMN DROP NOT NULL failed: %v", err)
	}
	tbl3, _ := eng.GetTable("employees")
	colName, _, _ := tbl3.FindColumn("name")
	if !colName.Nullable {
		t.Fatalf("expected name to be nullable after DROP NOT NULL")
	}

	// 13. ALTER TABLE DROP CONSTRAINT
	_, err = coord.ExecuteSQL(`ALTER TABLE employees DROP CONSTRAINT IF EXISTS uq_emp_email;`)
	if err != nil {
		t.Fatalf("ALTER TABLE DROP CONSTRAINT failed: %v", err)
	}

	// 14. ALTER TABLE DROP COLUMN
	_, err = coord.ExecuteSQL(`ALTER TABLE employees DROP COLUMN IF EXISTS department;`)
	if err != nil {
		t.Fatalf("ALTER TABLE DROP COLUMN failed: %v", err)
	}

	// 15. ALTER TABLE IF EXISTS on non-existent table should succeed cleanly
	_, err = coord.ExecuteSQL(`ALTER TABLE IF EXISTS non_existent_table ADD COLUMN foo INT;`)
	if err != nil {
		t.Fatalf("ALTER TABLE IF EXISTS on non-existent table failed: %v", err)
	}

	// 16. CREATE UNIQUE INDEX IF NOT EXISTS with WHERE
	_, err = coord.ExecuteSQL(`CREATE UNIQUE INDEX IF NOT EXISTS idx_emp_uniq_email ON employees (email) WHERE (email IS NOT NULL);`)
	if err != nil {
		t.Fatalf("CREATE UNIQUE INDEX IF NOT EXISTS with WHERE failed: %v", err)
	}

	// Re-executing CREATE UNIQUE INDEX IF NOT EXISTS should succeed idempotently
	_, err = coord.ExecuteSQL(`CREATE UNIQUE INDEX IF NOT EXISTS idx_emp_uniq_email ON employees (email) WHERE (email IS NOT NULL);`)
	if err != nil {
		t.Fatalf("re-executing CREATE UNIQUE INDEX IF NOT EXISTS failed: %v", err)
	}

	// 17. DROP INDEX IF EXISTS
	_, err = coord.ExecuteSQL(`DROP INDEX IF EXISTS idx_emp_uniq_email CASCADE;`)
	if err != nil {
		t.Fatalf("DROP INDEX IF EXISTS failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`DROP INDEX IF EXISTS non_existent_index;`)
	if err != nil {
		t.Fatalf("DROP INDEX IF EXISTS on non-existent index failed: %v", err)
	}

	// 18. ALTER TABLE ALTER COLUMN SET DEFAULT & DROP DEFAULT
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ALTER COLUMN salary SET DEFAULT 1000.0;`)
	if err != nil {
		t.Fatalf("ALTER COLUMN SET DEFAULT failed: %v", err)
	}
	tbl4, _ := eng.GetTable("employees")
	colSalary, _, _ := tbl4.FindColumn("salary")
	if colSalary.Default == "" {
		t.Fatalf("expected salary default to be set")
	}

	_, err = coord.ExecuteSQL(`ALTER TABLE employees ALTER COLUMN salary DROP DEFAULT;`)
	if err != nil {
		t.Fatalf("ALTER COLUMN DROP DEFAULT failed: %v", err)
	}
	tbl5, _ := eng.GetTable("employees")
	colSalary5, _, _ := tbl5.FindColumn("salary")
	if colSalary5.Default != "" {
		t.Fatalf("expected salary default to be cleared")
	}

	// 19. ALTER TABLE ALTER COLUMN TYPE ... USING ...
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ALTER COLUMN email TYPE varchar(300) USING email;`)
	if err != nil {
		t.Fatalf("ALTER COLUMN TYPE failed: %v", err)
	}

	// 20. ALTER TABLE RENAME COLUMN
	_, err = coord.ExecuteSQL(`ALTER TABLE employees RENAME COLUMN email TO contact_email;`)
	if err != nil {
		t.Fatalf("ALTER TABLE RENAME COLUMN failed: %v", err)
	}
	tbl6, _ := eng.GetTable("employees")
	if _, _, found := tbl6.FindColumn("contact_email"); !found {
		t.Fatalf("expected contact_email column to exist after rename")
	}

	// 21. Multi-column DROP COLUMN
	_, err = coord.ExecuteSQL(`ALTER TABLE employees DROP COLUMN IF EXISTS contact_email, DROP COLUMN IF EXISTS salary;`)
	if err != nil {
		t.Fatalf("multi DROP COLUMN failed: %v", err)
	}
	tbl7, _ := eng.GetTable("employees")
	if _, _, found := tbl7.FindColumn("contact_email"); found {
		t.Fatalf("contact_email should have been dropped")
	}
	if _, _, found := tbl7.FindColumn("salary"); found {
		t.Fatalf("salary should have been dropped")
	}

	// 22. ALTER TABLE ADD PRIMARY KEY
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ADD PRIMARY KEY (id);`)
	if err != nil {
		t.Fatalf("ADD PRIMARY KEY failed: %v", err)
	}

	// 23. ALTER TABLE ADD COLUMN IF NOT EXISTS (idempotent)
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ADD COLUMN IF NOT EXISTS phone VARCHAR(255);`)
	if err != nil {
		t.Fatalf("ADD COLUMN IF NOT EXISTS failed: %v", err)
	}
	_, err = coord.ExecuteSQL(`ALTER TABLE employees ADD COLUMN IF NOT EXISTS phone VARCHAR(255);`)
	if err != nil {
		t.Fatalf("re-executing ADD COLUMN IF NOT EXISTS should succeed idempotently: %v", err)
	}

	// 24. Multiple ADD COLUMN separated by comma
	_, err = coord.ExecuteSQL(`ALTER TABLE employees
		ADD COLUMN city VARCHAR(100),
		ADD COLUMN country VARCHAR(100);`)
	if err != nil {
		t.Fatalf("multiple ADD COLUMN failed: %v", err)
	}
	tbl8, _ := eng.GetTable("employees")
	if _, _, found := tbl8.FindColumn("city"); !found {
		t.Fatalf("expected city column to exist")
	}
	if _, _, found := tbl8.FindColumn("country"); !found {
		t.Fatalf("expected country column to exist")
	}
}
