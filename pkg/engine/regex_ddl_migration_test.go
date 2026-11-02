package engine

import (
	"testing"
	"time"

	"lionsql/pkg/catalog"
	"lionsql/pkg/storage/memengine"
)

func TestRegexDDLMigration_E2E(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	statements := []string{
		// 1. CREATE TABLE with bracketed names, DEFAULT with string literal, DECIMAL precision/scale
		"CREATE TABLE [dbo].[Accounts] ( [id] INT PRIMARY KEY, [owner_name] VARCHAR(100) DEFAULT 'Anonymous', [balance] DECIMAL(18, 4) DEFAULT 0.00, [created_at] TIMESTAMP )",

		// 2. CREATE TABLE IF NOT EXISTS
		"CREATE TABLE IF NOT EXISTS [dbo].[Accounts] ( [id] INT PRIMARY KEY )",

		// 3. CREATE INDEX with bracketed table and column
		"CREATE INDEX [idx_owner] ON [dbo].[Accounts] ([owner_name])",

		// 4. CREATE UNIQUE INDEX IF NOT EXISTS
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_acc_id ON [Accounts] (id)",

		// 5. ALTER TABLE ADD COLUMN
		"ALTER TABLE [dbo].[Accounts] ADD COLUMN status VARCHAR(20) DEFAULT 'ACTIVE'",

		// 6. ALTER TABLE ADD COLUMN IF NOT EXISTS
		"ALTER TABLE [dbo].[Accounts] ADD COLUMN IF NOT EXISTS status VARCHAR(20)",

		// 7. CREATE VIEW
		"CREATE VIEW [v_active_accounts] AS SELECT id, owner_name, balance FROM Accounts WHERE status = 'ACTIVE'",

		// 8. CREATE OR REPLACE VIEW
		"CREATE OR REPLACE VIEW [v_active_accounts] AS SELECT id, owner_name, balance, status FROM Accounts",

		// 9. INSERT and SELECT
		"INSERT INTO Accounts (id, owner_name, balance, status) VALUES (1, 'Alice', 1500.50, 'ACTIVE')",
		"INSERT INTO Accounts (id, owner_name, balance, status) VALUES (2, 'Bob', 2500.00, 'INACTIVE')",

		// 10. DROP INDEX
		"DROP INDEX idx_owner ON [dbo].[Accounts]",

		// 11. DROP VIEW
		"DROP VIEW IF EXISTS [v_active_accounts]",

		// 12. TRUNCATE TABLE
		"TRUNCATE TABLE [Accounts]",

		// 13. DROP TABLE
		"DROP TABLE IF EXISTS [dbo].[Accounts]",
	}

	for _, stmt := range statements {
		res, err := coord.ExecuteSQL(stmt)
		if err != nil {
			t.Fatalf("ExecuteSQL(%q) failed: %v", stmt, err)
		}
		if !res.Success {
			t.Fatalf("ExecuteSQL(%q) returned success=false", stmt)
		}
	}
}

func TestRegexDDLMigration_ExplainSubqueries(t *testing.T) {
	eng := memengine.New()
	cat := catalog.New(eng)
	coord := NewCoordinator(cat, eng, Config{})
	defer coord.Close()

	_, err := coord.ExecuteSQL("CREATE TABLE dept (id INT PRIMARY KEY, name VARCHAR(50))")
	if err != nil {
		t.Fatalf("CREATE TABLE dept: %v", err)
	}
	_, err = coord.ExecuteSQL("CREATE TABLE emp (id INT PRIMARY KEY, dept_id INT, name VARCHAR(50))")
	if err != nil {
		t.Fatalf("CREATE TABLE emp: %v", err)
	}

	queries := []string{
		"EXPLAIN SELECT * FROM emp WHERE dept_id IN (SELECT id FROM dept WHERE name = 'Sales')",
		"EXPLAIN SELECT * FROM emp WHERE EXISTS (SELECT 1 FROM dept WHERE dept.id = emp.dept_id)",
		"EXPLAIN SELECT (SELECT name FROM dept WHERE dept.id = emp.dept_id) FROM emp",
	}

	for _, q := range queries {
		res, err := coord.execExplain(q, time.Now())
		if err != nil {
			t.Fatalf("execExplain(%q) failed: %v", q, err)
		}
		if !res.Success || len(res.Rows) == 0 {
			t.Fatalf("execExplain(%q) expected rows, got: %+v", q, res)
		}
	}
}
